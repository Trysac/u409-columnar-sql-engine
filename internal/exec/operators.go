package exec

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

// BaseOperator provides common operator metadata.
type BaseOperator struct {
	schema      []interfaces.DataType
	columnNames []string
}

func (b *BaseOperator) Schema() []interfaces.DataType {
	return b.schema
}

func (b *BaseOperator) ColumnNames() []string {
	return b.columnNames
}

// ScanOperator emits batches from an underlying BatchReader.
type ScanOperator struct {
	BaseOperator
	reader interfaces.BatchReader
}

// NewScanOperator creates a scan operator wrapping a BatchReader.
func NewScanOperator(reader interfaces.BatchReader) *ScanOperator {
	return &ScanOperator{
		BaseOperator: BaseOperator{
			schema:      reader.Schema(),
			columnNames: reader.ColumnNames(),
		},
		reader: reader,
	}
}

func (s *ScanOperator) Init(ctx context.Context) error {
	return nil
}

func (s *ScanOperator) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.reader.Next(ctx)
}

func (s *ScanOperator) Close() error {
	if s.reader != nil {
		return s.reader.Close()
	}
	return nil
}

// ValuesOperator yields a single synthesized batch (e.g., SELECT without FROM).
type ValuesOperator struct {
	BaseOperator
	batch    *interfaces.RecordBatch
	consumed bool
}

// NewValuesOperator creates a values operator with 1 row.
func NewValuesOperator() *ValuesOperator {
	return &ValuesOperator{
		BaseOperator: BaseOperator{
			schema:      nil,
			columnNames: nil,
		},
		batch: &interfaces.RecordBatch{
			Columns: nil,
			Length:  1,
		},
	}
}

func (v *ValuesOperator) Init(ctx context.Context) error {
	v.consumed = false
	return nil
}

func (v *ValuesOperator) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	if v.consumed {
		return nil, nil
	}
	v.consumed = true
	return v.batch, nil
}

func (v *ValuesOperator) Close() error {
	return nil
}

// FilterOperator filters rows using a predicate.
type FilterOperator struct {
	BaseOperator
	child     interfaces.Operator
	predicate interfaces.Expr
	evaluator interfaces.VectorEvaluator
}

// NewFilterOperator creates a filter operator.
func NewFilterOperator(child interfaces.Operator, predicate interfaces.Expr, evaluator interfaces.VectorEvaluator) *FilterOperator {
	return &FilterOperator{
		BaseOperator: BaseOperator{
			schema:      child.Schema(),
			columnNames: child.ColumnNames(),
		},
		child:     child,
		predicate: predicate,
		evaluator: evaluator,
	}
}

func (f *FilterOperator) Init(ctx context.Context) error {
	return f.child.Init(ctx)
}

func (f *FilterOperator) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := f.child.Next(ctx)
		if err != nil || batch == nil {
			return nil, err
		}
		if batch.Length == 0 {
			continue
		}
		filterCol, err := f.evaluator.Evaluate(ctx, f.predicate, batch, f.child.ColumnNames())
		if err != nil {
			return nil, err
		}
		var selIndices []int
		for i := 0; i < batch.Length; i++ {
			if !filterCol.NullMask[i] && filterCol.Bools[i] {
				selIndices = append(selIndices, i)
			}
		}
		if len(selIndices) == 0 {
			continue
		}
		return sliceBatch(batch, selIndices), nil
	}
}

func (f *FilterOperator) Close() error {
	return f.child.Close()
}

// ProjectionOperator evaluates projection expressions over child batches.
type ProjectionOperator struct {
	BaseOperator
	child       interfaces.Operator
	projections []interfaces.SelectItem
	evaluator   interfaces.VectorEvaluator
}

// NewProjectionOperator creates a projection operator.
func NewProjectionOperator(child interfaces.Operator, projections []interfaces.SelectItem, evaluator interfaces.VectorEvaluator) *ProjectionOperator {
	colNames := make([]string, len(projections))
	for i, p := range projections {
		if p.Alias != "" {
			colNames[i] = p.Alias
		} else if cr, ok := p.Expr.(interfaces.ColumnRef); ok {
			colNames[i] = cr.Name
		} else {
			colNames[i] = fmt.Sprintf("col_%d", i)
		}
	}
	return &ProjectionOperator{
		BaseOperator: BaseOperator{
			columnNames: colNames,
		},
		child:       child,
		projections: projections,
		evaluator:   evaluator,
	}
}

func (p *ProjectionOperator) Init(ctx context.Context) error {
	return p.child.Init(ctx)
}

func (p *ProjectionOperator) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	batch, err := p.child.Next(ctx)
	if err != nil || batch == nil {
		return nil, err
	}

	cols := make([]interfaces.ColumnVector, len(p.projections))
	for i, proj := range p.projections {
		vec, err := p.evaluator.Evaluate(ctx, proj.Expr, batch, p.child.ColumnNames())
		if err != nil {
			return nil, err
		}
		cols[i] = *vec
	}
	if len(p.schema) == 0 {
		p.schema = make([]interfaces.DataType, len(cols))
		for i := range cols {
			p.schema[i] = cols[i].Type
		}
	}
	return &interfaces.RecordBatch{
		Columns: cols,
		Length:  batch.Length,
	}, nil
}

func (p *ProjectionOperator) Close() error {
	return p.child.Close()
}

// HashAggregateOperator performs grouped and ungrouped hash aggregations.
type HashAggregateOperator struct {
	BaseOperator
	child       interfaces.Operator
	groupExprs  []interfaces.Expr
	aggs        []interfaces.AggregateCall
	evaluator   interfaces.VectorEvaluator
	resultBatch *interfaces.RecordBatch
	emitted     bool
}

// NewHashAggregateOperator creates a hash aggregate operator.
func NewHashAggregateOperator(
	child interfaces.Operator,
	groupExprs []interfaces.Expr,
	aggs []interfaces.AggregateCall,
	columnNames []string,
	schema []interfaces.DataType,
	evaluator interfaces.VectorEvaluator,
) *HashAggregateOperator {
	return &HashAggregateOperator{
		BaseOperator: BaseOperator{
			schema:      schema,
			columnNames: columnNames,
		},
		child:      child,
		groupExprs: groupExprs,
		aggs:       aggs,
		evaluator:  evaluator,
	}
}

type aggAccumulator struct {
	count    int64
	sum      float64
	hasSum   bool
	minVal   any
	maxVal   any
	distinct map[any]struct{}
}

type groupEntry struct {
	keys []any
	aggs []*aggAccumulator
}

func (h *HashAggregateOperator) Init(ctx context.Context) error {
	h.emitted = false
	h.resultBatch = nil
	if err := h.child.Init(ctx); err != nil {
		return err
	}

	groupsMap := make(map[string]*groupEntry)
	var groupList []*groupEntry
	childCols := h.child.ColumnNames()
	hasInputRows := false

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := h.child.Next(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			break
		}
		if batch.Length == 0 {
			continue
		}
		hasInputRows = true

		groupVectors := make([]*interfaces.ColumnVector, len(h.groupExprs))
		for i, gExpr := range h.groupExprs {
			gv, err := h.evaluator.Evaluate(ctx, gExpr, batch, childCols)
			if err != nil {
				return err
			}
			groupVectors[i] = gv
		}

		aggVectors := make([]*interfaces.ColumnVector, len(h.aggs))
		for i, agg := range h.aggs {
			if len(agg.Args) > 0 {
				av, err := h.evaluator.Evaluate(ctx, agg.Args[0], batch, childCols)
				if err != nil {
					return err
				}
				aggVectors[i] = av
			}
		}

		for rowIdx := 0; rowIdx < batch.Length; rowIdx++ {
			keyStr := ""
			keys := make([]any, len(h.groupExprs))
			for kIdx, gv := range groupVectors {
				val := getVectorValue(gv, rowIdx)
				keys[kIdx] = val
				keyStr += fmt.Sprintf("%v\x00", val)
			}

			entry, exists := groupsMap[keyStr]
			if !exists {
				accs := make([]*aggAccumulator, len(h.aggs))
				for aIdx, agg := range h.aggs {
					acc := &aggAccumulator{}
					if agg.Distinct {
						acc.distinct = make(map[any]struct{})
					}
					accs[aIdx] = acc
				}
				entry = &groupEntry{keys: keys, aggs: accs}
				groupsMap[keyStr] = entry
				groupList = append(groupList, entry)
			}

			for aIdx, agg := range h.aggs {
				acc := entry.aggs[aIdx]
				if len(agg.Args) == 0 {
					// COUNT(*)
					acc.count++
					continue
				}
				argVec := aggVectors[aIdx]
				val := getVectorValue(argVec, rowIdx)
				if val == nil {
					continue
				}
				if agg.Distinct {
					if _, seen := acc.distinct[val]; seen {
						continue
					}
					acc.distinct[val] = struct{}{}
				}
				acc.count++
				fVal, ok := toFloat64(val)
				if ok {
					acc.sum += fVal
					acc.hasSum = true
				}
				if acc.minVal == nil || CompareValues(val, acc.minVal) < 0 {
					acc.minVal = val
				}
				if acc.maxVal == nil || CompareValues(val, acc.maxVal) > 0 {
					acc.maxVal = val
				}
			}
		}
	}

	if len(h.groupExprs) == 0 && !hasInputRows {
		accs := make([]*aggAccumulator, len(h.aggs))
		for aIdx, agg := range h.aggs {
			acc := &aggAccumulator{}
			if agg.Distinct {
				acc.distinct = make(map[any]struct{})
			}
			accs[aIdx] = acc
		}
		groupList = append(groupList, &groupEntry{keys: nil, aggs: accs})
	}

	h.resultBatch = buildAggregateBatch(groupList, h.groupExprs, h.aggs, h.schema)
	return nil
}

func (h *HashAggregateOperator) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	if h.emitted || h.resultBatch == nil {
		return nil, nil
	}
	h.emitted = true
	return h.resultBatch, nil
}

func (h *HashAggregateOperator) Close() error {
	return h.child.Close()
}

func buildAggregateBatch(groupList []*groupEntry, groupExprs []interfaces.Expr, aggs []interfaces.AggregateCall, schema []interfaces.DataType) *interfaces.RecordBatch {
	numRows := len(groupList)
	totalCols := len(groupExprs) + len(aggs)
	cols := make([]interfaces.ColumnVector, totalCols)

	for cIdx := 0; cIdx < len(groupExprs); cIdx++ {
		typeCol := interfaces.TypeVarChar
		if cIdx < len(schema) {
			typeCol = schema[cIdx]
		}
		cols[cIdx] = allocateColumn(typeCol, numRows)
		for rIdx, g := range groupList {
			setVectorValue(&cols[cIdx], rIdx, g.keys[cIdx])
		}
	}

	for aIdx, agg := range aggs {
		cIdx := len(groupExprs) + aIdx
		typeCol := interfaces.TypeFloat64
		if cIdx < len(schema) {
			typeCol = schema[cIdx]
		}
		cols[cIdx] = allocateColumn(typeCol, numRows)
		upperFunc := agg.Func
		for rIdx, g := range groupList {
			acc := g.aggs[aIdx]
			switch upperFunc {
			case "COUNT":
				setVectorValue(&cols[cIdx], rIdx, acc.count)
			case "SUM":
				if !acc.hasSum {
					setVectorValue(&cols[cIdx], rIdx, nil)
				} else {
					if typeCol == interfaces.TypeInt64 {
						setVectorValue(&cols[cIdx], rIdx, int64(acc.sum))
					} else {
						setVectorValue(&cols[cIdx], rIdx, acc.sum)
					}
				}
			case "AVG":
				if acc.count == 0 || !acc.hasSum {
					setVectorValue(&cols[cIdx], rIdx, nil)
				} else {
					setVectorValue(&cols[cIdx], rIdx, acc.sum/float64(acc.count))
				}
			case "MIN":
				setVectorValue(&cols[cIdx], rIdx, acc.minVal)
			case "MAX":
				setVectorValue(&cols[cIdx], rIdx, acc.maxVal)
			}
		}
	}

	return &interfaces.RecordBatch{
		Columns: cols,
		Length:  numRows,
	}
}

// HashJoinOperator performs vectorized inner and left equijoins.
type HashJoinOperator struct {
	BaseOperator
	left         interfaces.Operator
	right        interfaces.Operator
	joinType     interfaces.JoinType
	leftKey      interfaces.Expr
	rightKey     interfaces.Expr
	evaluator    interfaces.VectorEvaluator
	batchSize    int
	emittedBatch *interfaces.RecordBatch
	emitted      bool
}

// NewHashJoinOperator creates a hash join operator.
func NewHashJoinOperator(
	left interfaces.Operator,
	right interfaces.Operator,
	joinType interfaces.JoinType,
	leftKey interfaces.Expr,
	rightKey interfaces.Expr,
	columnNames []string,
	schema []interfaces.DataType,
	evaluator interfaces.VectorEvaluator,
	batchSize int,
) *HashJoinOperator {
	if batchSize <= 0 {
		batchSize = 1024
	}
	return &HashJoinOperator{
		BaseOperator: BaseOperator{
			schema:      schema,
			columnNames: columnNames,
		},
		left:      left,
		right:     right,
		joinType:  joinType,
		leftKey:   leftKey,
		rightKey:  rightKey,
		evaluator: evaluator,
		batchSize: batchSize,
	}
}

func (h *HashJoinOperator) Init(ctx context.Context) error {
	h.emitted = false
	h.emittedBatch = nil
	if err := h.left.Init(ctx); err != nil {
		return err
	}
	if err := h.right.Init(ctx); err != nil {
		return err
	}

	var leftBatches []*interfaces.RecordBatch
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := h.left.Next(ctx)
		if err != nil {
			return err
		}
		if b == nil {
			break
		}
		if b.Length > 0 {
			leftBatches = append(leftBatches, b)
		}
	}

	var rightBatches []*interfaces.RecordBatch
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := h.right.Next(ctx)
		if err != nil {
			return err
		}
		if b == nil {
			break
		}
		if b.Length > 0 {
			rightBatches = append(rightBatches, b)
		}
	}

	type rightPos struct {
		batchIdx int
		rowIdx   int
	}
	rightHashTable := make(map[string][]rightPos)
	rightCols := h.right.ColumnNames()
	for bIdx, rb := range rightBatches {
		rKeyVec, err := h.evaluator.Evaluate(ctx, h.rightKey, rb, rightCols)
		if err != nil {
			return err
		}
		for rIdx := 0; rIdx < rb.Length; rIdx++ {
			if rKeyVec.NullMask[rIdx] {
				// In SQL, NULL does not match NULL in joins.
				continue
			}
			val := getVectorValue(rKeyVec, rIdx)
			keyStr := fmt.Sprintf("%v", val)
			rightHashTable[keyStr] = append(rightHashTable[keyStr], rightPos{batchIdx: bIdx, rowIdx: rIdx})
		}
	}

	type matchPair struct {
		lBatchIdx   int
		lRowIdx     int
		rBatchIdx   int
		rRowIdx     int
		isRightNull bool
	}
	var matches []matchPair

	leftCols := h.left.ColumnNames()
	for bIdx, lb := range leftBatches {
		lKeyVec, err := h.evaluator.Evaluate(ctx, h.leftKey, lb, leftCols)
		if err != nil {
			return err
		}
		for lIdx := 0; lIdx < lb.Length; lIdx++ {
			if lKeyVec.NullMask[lIdx] {
				if h.joinType == interfaces.JoinLeft {
					matches = append(matches, matchPair{
						lBatchIdx:   bIdx,
						lRowIdx:     lIdx,
						isRightNull: true,
					})
				}
				continue
			}
			lVal := getVectorValue(lKeyVec, lIdx)
			keyStr := fmt.Sprintf("%v", lVal)
			rMatches, found := rightHashTable[keyStr]
			if found && len(rMatches) > 0 {
				for _, rm := range rMatches {
					matches = append(matches, matchPair{
						lBatchIdx:   bIdx,
						lRowIdx:     lIdx,
						rBatchIdx:   rm.batchIdx,
						rRowIdx:     rm.rowIdx,
						isRightNull: false,
					})
				}
			} else if h.joinType == interfaces.JoinLeft {
				matches = append(matches, matchPair{
					lBatchIdx:   bIdx,
					lRowIdx:     lIdx,
					isRightNull: true,
				})
			}
		}
	}

	numRows := len(matches)
	totalCols := len(h.schema)
	cols := make([]interfaces.ColumnVector, totalCols)
	for cIdx := 0; cIdx < totalCols; cIdx++ {
		cols[cIdx] = allocateColumn(h.schema[cIdx], numRows)
	}

	leftColCount := len(h.left.Schema())
	for mIdx, m := range matches {
		lBatch := leftBatches[m.lBatchIdx]
		for cIdx := 0; cIdx < leftColCount; cIdx++ {
			val := getVectorValue(&lBatch.Columns[cIdx], m.lRowIdx)
			setVectorValue(&cols[cIdx], mIdx, val)
		}
		if m.isRightNull {
			for cIdx := leftColCount; cIdx < totalCols; cIdx++ {
				setVectorValue(&cols[cIdx], mIdx, nil)
			}
		} else {
			rBatch := rightBatches[m.rBatchIdx]
			for cIdx := 0; cIdx < len(h.right.Schema()); cIdx++ {
				val := getVectorValue(&rBatch.Columns[cIdx], m.rRowIdx)
				setVectorValue(&cols[leftColCount+cIdx], mIdx, val)
			}
		}
	}

	h.emittedBatch = &interfaces.RecordBatch{
		Columns: cols,
		Length:  numRows,
	}
	return nil
}

func (h *HashJoinOperator) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	if h.emitted || h.emittedBatch == nil {
		return nil, nil
	}
	h.emitted = true
	return h.emittedBatch, nil
}

func (h *HashJoinOperator) Close() error {
	err1 := h.left.Close()
	err2 := h.right.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// SortOperator sorts input batches with NULLS FIRST / NULLS LAST support.
type SortOperator struct {
	BaseOperator
	child       interfaces.Operator
	orderBy     []interfaces.OrderByItem
	evaluator   interfaces.VectorEvaluator
	sortedBatch *interfaces.RecordBatch
	emitted     bool
}

// NewSortOperator creates a sort operator.
func NewSortOperator(child interfaces.Operator, orderBy []interfaces.OrderByItem, evaluator interfaces.VectorEvaluator) *SortOperator {
	return &SortOperator{
		BaseOperator: BaseOperator{
			schema:      child.Schema(),
			columnNames: child.ColumnNames(),
		},
		child:     child,
		orderBy:   orderBy,
		evaluator: evaluator,
	}
}

func (s *SortOperator) Init(ctx context.Context) error {
	s.emitted = false
	s.sortedBatch = nil
	if err := s.child.Init(ctx); err != nil {
		return err
	}

	var batches []*interfaces.RecordBatch
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := s.child.Next(ctx)
		if err != nil {
			return err
		}
		if b == nil {
			break
		}
		if b.Length > 0 {
			batches = append(batches, b)
		}
	}

	merged := mergeBatches(batches, s.schema)
	if merged.Length == 0 {
		s.sortedBatch = merged
		return nil
	}

	orderVectors := make([]*interfaces.ColumnVector, len(s.orderBy))
	for i, ob := range s.orderBy {
		ov, err := s.evaluator.Evaluate(ctx, ob.Expr, merged, s.columnNames)
		if err != nil {
			return err
		}
		orderVectors[i] = ov
	}

	indices := make([]int, merged.Length)
	for i := range indices {
		indices[i] = i
	}

	sort.SliceStable(indices, func(i, j int) bool {
		rowA := indices[i]
		rowB := indices[j]
		for k, ob := range s.orderBy {
			vec := orderVectors[k]
			aNull := vec.NullMask[rowA]
			bNull := vec.NullMask[rowB]
			if aNull && bNull {
				continue
			}
			if aNull {
				if ob.NullsFirst {
					return true
				}
				return false
			}
			if bNull {
				if ob.NullsFirst {
					return false
				}
				return true
			}
			valA := getVectorValue(vec, rowA)
			valB := getVectorValue(vec, rowB)
			cmp := CompareValues(valA, valB)
			if cmp != 0 {
				if ob.Ascending {
					return cmp < 0
				}
				return cmp > 0
			}
		}
		return false
	})

	s.sortedBatch = sliceBatch(merged, indices)
	return nil
}

func (s *SortOperator) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	if s.emitted || s.sortedBatch == nil {
		return nil, nil
	}
	s.emitted = true
	return s.sortedBatch, nil
}

func (s *SortOperator) Close() error {
	return s.child.Close()
}

// LimitOperator limits the number of rows emitted with optional offset.
type LimitOperator struct {
	BaseOperator
	child   interfaces.Operator
	limit   int64
	offset  int64
	skipped int64
	emitted int64
}

// NewLimitOperator creates a limit operator.
func NewLimitOperator(child interfaces.Operator, limit *int64, offset *int64) *LimitOperator {
	var lim int64 = math.MaxInt64
	if limit != nil {
		lim = *limit
	}
	var off int64 = 0
	if offset != nil {
		off = *offset
	}
	return &LimitOperator{
		BaseOperator: BaseOperator{
			schema:      child.Schema(),
			columnNames: child.ColumnNames(),
		},
		child:  child,
		limit:  lim,
		offset: off,
	}
}

func (l *LimitOperator) Init(ctx context.Context) error {
	l.skipped = 0
	l.emitted = 0
	return l.child.Init(ctx)
}

func (l *LimitOperator) Next(ctx context.Context) (*interfaces.RecordBatch, error) {
	if l.emitted >= l.limit {
		return nil, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := l.child.Next(ctx)
		if err != nil || batch == nil {
			return nil, err
		}
		if batch.Length == 0 {
			continue
		}

		startIdx := 0
		if l.skipped < l.offset {
			needed := l.offset - l.skipped
			if int64(batch.Length) <= needed {
				l.skipped += int64(batch.Length)
				continue
			}
			startIdx = int(needed)
			l.skipped = l.offset
		}

		remainingLimit := l.limit - l.emitted
		endIdx := batch.Length
		if int64(endIdx-startIdx) > remainingLimit {
			endIdx = startIdx + int(remainingLimit)
		}

		size := endIdx - startIdx
		if size <= 0 {
			return nil, nil
		}

		sel := make([]int, size)
		for i := 0; i < size; i++ {
			sel[i] = startIdx + i
		}
		l.emitted += int64(size)
		return sliceBatch(batch, sel), nil
	}
}

func (l *LimitOperator) Close() error {
	return l.child.Close()
}

// Slicing and batch utility functions

func sliceBatch(batch *interfaces.RecordBatch, indices []int) *interfaces.RecordBatch {
	newCols := make([]interfaces.ColumnVector, len(batch.Columns))
	for cIdx, col := range batch.Columns {
		newCols[cIdx] = allocateColumn(col.Type, len(indices))
		for outIdx, inIdx := range indices {
			if col.NullMask[inIdx] {
				newCols[cIdx].NullMask[outIdx] = true
			} else {
				copyVectorElem(&newCols[cIdx], outIdx, &col, inIdx)
			}
		}
	}
	return &interfaces.RecordBatch{
		Columns: newCols,
		Length:  len(indices),
	}
}

func mergeBatches(batches []*interfaces.RecordBatch, schema []interfaces.DataType) *interfaces.RecordBatch {
	totalRows := 0
	for _, b := range batches {
		totalRows += b.Length
	}
	if len(batches) == 0 {
		cols := make([]interfaces.ColumnVector, len(schema))
		for i, dt := range schema {
			cols[i] = allocateColumn(dt, 0)
		}
		return &interfaces.RecordBatch{Columns: cols, Length: 0}
	}
	numCols := len(batches[0].Columns)
	cols := make([]interfaces.ColumnVector, numCols)
	for cIdx := 0; cIdx < numCols; cIdx++ {
		dt := batches[0].Columns[cIdx].Type
		cols[cIdx] = allocateColumn(dt, totalRows)
		currRow := 0
		for _, b := range batches {
			col := &b.Columns[cIdx]
			for r := 0; r < b.Length; r++ {
				if col.NullMask[r] {
					cols[cIdx].NullMask[currRow] = true
				} else {
					copyVectorElem(&cols[cIdx], currRow, col, r)
				}
				currRow++
			}
		}
	}
	return &interfaces.RecordBatch{
		Columns: cols,
		Length:  totalRows,
	}
}

func allocateColumn(dt interfaces.DataType, length int) interfaces.ColumnVector {
	res := interfaces.ColumnVector{
		Type:     dt,
		Length:   length,
		NullMask: make([]bool, length),
	}
	switch dt {
	case interfaces.TypeInt64:
		res.Int64s = make([]int64, length)
	case interfaces.TypeFloat64, interfaces.TypeDecimal:
		res.Float64s = make([]float64, length)
	case interfaces.TypeVarChar, interfaces.TypeDate:
		res.Strings = make([]string, length)
	case interfaces.TypeBool:
		res.Bools = make([]bool, length)
	}
	return res
}

func getVectorValue(vec *interfaces.ColumnVector, idx int) any {
	if vec.NullMask[idx] {
		return nil
	}
	switch vec.Type {
	case interfaces.TypeInt64:
		if idx < len(vec.Int64s) {
			return vec.Int64s[idx]
		}
	case interfaces.TypeFloat64, interfaces.TypeDecimal:
		if idx < len(vec.Float64s) {
			return vec.Float64s[idx]
		}
	case interfaces.TypeVarChar, interfaces.TypeDate:
		if idx < len(vec.Strings) {
			return vec.Strings[idx]
		}
	case interfaces.TypeBool:
		if idx < len(vec.Bools) {
			return vec.Bools[idx]
		}
	}
	return nil
}

func setVectorValue(vec *interfaces.ColumnVector, idx int, val any) {
	if val == nil {
		vec.NullMask[idx] = true
		return
	}
	vec.NullMask[idx] = false
	switch vec.Type {
	case interfaces.TypeInt64:
		i, ok := toInt64(val)
		if ok {
			vec.Int64s[idx] = i
		}
	case interfaces.TypeFloat64, interfaces.TypeDecimal:
		f, ok := toFloat64(val)
		if ok {
			vec.Float64s[idx] = f
		}
	case interfaces.TypeVarChar, interfaces.TypeDate:
		vec.Strings[idx] = fmt.Sprintf("%v", val)
	case interfaces.TypeBool:
		b, ok := toBool(val)
		if ok {
			vec.Bools[idx] = b
		}
	}
}
