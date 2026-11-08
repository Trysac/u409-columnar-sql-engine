package exec

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

// Evaluator implements interfaces.VectorEvaluator.
type Evaluator struct{}

// NewEvaluator creates a new VectorEvaluator.
func NewEvaluator() interfaces.VectorEvaluator {
	return &Evaluator{}
}

// Evaluate evaluates an expression over a record batch.
func (e *Evaluator) Evaluate(ctx context.Context, expr interfaces.Expr, batch *interfaces.RecordBatch, columnNames []string) (*interfaces.ColumnVector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch ex := expr.(type) {
	case interfaces.ColumnRef:
		idx := -1
		for i, name := range columnNames {
			if name == ex.Name || (ex.Table != "" && name == ex.Table+"."+ex.Name) {
				idx = i
				break
			}
			if dotIdx := strings.LastIndex(name, "."); dotIdx >= 0 && name[dotIdx+1:] == ex.Name {
				if ex.Table == "" || strings.HasPrefix(name, ex.Table+".") {
					idx = i
					break
				}
			}
		}
		if idx == -1 {
			return nil, &interfaces.EngineError{
				Code:    interfaces.ErrColumnNotFound,
				Message: fmt.Sprintf("column %s not found", ex.Name),
			}
		}
		if idx >= len(batch.Columns) {
			return nil, &interfaces.EngineError{
				Code:    interfaces.ErrColumnNotFound,
				Message: fmt.Sprintf("column %s index out of bounds", ex.Name),
			}
		}
		col := batch.Columns[idx]
		return &col, nil

	case interfaces.LiteralExpr:
		length := batch.Length
		if length <= 0 {
			length = 1
		}
		res := interfaces.ColumnVector{
			Type:     ex.Type,
			Length:   length,
			NullMask: make([]bool, length),
		}
		if ex.IsNull {
			for i := 0; i < length; i++ {
				res.NullMask[i] = true
			}
			switch ex.Type {
			case interfaces.TypeInt64:
				res.Int64s = make([]int64, length)
			case interfaces.TypeFloat64, interfaces.TypeDecimal:
				res.Float64s = make([]float64, length)
			case interfaces.TypeVarChar, interfaces.TypeDate:
				res.Strings = make([]string, length)
			case interfaces.TypeBool:
				res.Bools = make([]bool, length)
			}
			return &res, nil
		}
		switch ex.Type {
		case interfaces.TypeInt64:
			res.Int64s = make([]int64, length)
			v, _ := toInt64(ex.Value)
			for i := 0; i < length; i++ {
				res.Int64s[i] = v
			}
		case interfaces.TypeFloat64, interfaces.TypeDecimal:
			res.Float64s = make([]float64, length)
			v, _ := toFloat64(ex.Value)
			for i := 0; i < length; i++ {
				res.Float64s[i] = v
			}
		case interfaces.TypeVarChar, interfaces.TypeDate:
			res.Strings = make([]string, length)
			v := fmt.Sprintf("%v", ex.Value)
			for i := 0; i < length; i++ {
				res.Strings[i] = v
			}
		case interfaces.TypeBool:
			res.Bools = make([]bool, length)
			v, _ := toBool(ex.Value)
			for i := 0; i < length; i++ {
				res.Bools[i] = v
			}
		}
		return &res, nil

	case interfaces.UnaryExpr:
		op, err := e.Evaluate(ctx, ex.Operand, batch, columnNames)
		if err != nil {
			return nil, err
		}
		return evaluateUnary(ex.Op, op)

	case interfaces.BinaryExpr:
		left, err := e.Evaluate(ctx, ex.Left, batch, columnNames)
		if err != nil {
			return nil, err
		}
		right, err := e.Evaluate(ctx, ex.Right, batch, columnNames)
		if err != nil {
			return nil, err
		}
		return evaluateBinary(ex.Op, left, right)

	case interfaces.CaseExpr:
		return e.evaluateCase(ctx, ex, batch, columnNames)

	case interfaces.CastExpr:
		val, err := e.Evaluate(ctx, ex.Expr, batch, columnNames)
		if err != nil {
			return nil, err
		}
		return evaluateCast(val, ex.TargetType)

	default:
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("unsupported expression type %T", expr),
		}
	}
}

func (e *Evaluator) evaluateCase(ctx context.Context, c interfaces.CaseExpr, batch *interfaces.RecordBatch, columnNames []string) (*interfaces.ColumnVector, error) {
	length := batch.Length
	if length == 0 {
		length = 1
	}
	var operandVec *interfaces.ColumnVector
	if c.Operand != nil {
		var err error
		operandVec, err = e.Evaluate(ctx, c.Operand, batch, columnNames)
		if err != nil {
			return nil, err
		}
	}

	resolvedIndices := make([]int, length)
	for i := range resolvedIndices {
		resolvedIndices[i] = -1
	}

	thenVectors := make([]*interfaces.ColumnVector, len(c.Whens))
	for wIdx, when := range c.Whens {
		var conditionMatches []bool
		if operandVec != nil {
			whenVec, err := e.Evaluate(ctx, when.When, batch, columnNames)
			if err != nil {
				return nil, err
			}
			eqVec, err := evaluateBinary(interfaces.OpEq, operandVec, whenVec)
			if err != nil {
				return nil, err
			}
			conditionMatches = make([]bool, length)
			for i := 0; i < length; i++ {
				if !eqVec.NullMask[i] && eqVec.Bools[i] {
					conditionMatches[i] = true
				}
			}
		} else {
			condVec, err := e.Evaluate(ctx, when.When, batch, columnNames)
			if err != nil {
				return nil, err
			}
			conditionMatches = make([]bool, length)
			for i := 0; i < length; i++ {
				if !condVec.NullMask[i] && condVec.Bools[i] {
					conditionMatches[i] = true
				}
			}
		}
		thenVec, err := e.Evaluate(ctx, when.Then, batch, columnNames)
		if err != nil {
			return nil, err
		}
		thenVectors[wIdx] = thenVec
		for i := 0; i < length; i++ {
			if resolvedIndices[i] == -1 && conditionMatches[i] {
				resolvedIndices[i] = wIdx
			}
		}
	}

	var elseVec *interfaces.ColumnVector
	if c.Else != nil {
		var err error
		elseVec, err = e.Evaluate(ctx, c.Else, batch, columnNames)
		if err != nil {
			return nil, err
		}
	}

	var outType interfaces.DataType
	if len(thenVectors) > 0 {
		outType = thenVectors[0].Type
	} else if elseVec != nil {
		outType = elseVec.Type
	} else {
		outType = interfaces.TypeVarChar
	}

	res := interfaces.ColumnVector{
		Type:     outType,
		Length:   length,
		NullMask: make([]bool, length),
	}
	switch outType {
	case interfaces.TypeInt64:
		res.Int64s = make([]int64, length)
	case interfaces.TypeFloat64, interfaces.TypeDecimal:
		res.Float64s = make([]float64, length)
	case interfaces.TypeVarChar, interfaces.TypeDate:
		res.Strings = make([]string, length)
	case interfaces.TypeBool:
		res.Bools = make([]bool, length)
	}

	for i := 0; i < length; i++ {
		idx := resolvedIndices[i]
		if idx >= 0 {
			vec := thenVectors[idx]
			if vec.NullMask[i] {
				res.NullMask[i] = true
			} else {
				copyVectorElem(&res, i, vec, i)
			}
		} else if elseVec != nil {
			if elseVec.NullMask[i] {
				res.NullMask[i] = true
			} else {
				copyVectorElem(&res, i, elseVec, i)
			}
		} else {
			res.NullMask[i] = true
		}
	}
	return &res, nil
}

func copyVectorElem(dst *interfaces.ColumnVector, dstIdx int, src *interfaces.ColumnVector, srcIdx int) {
	switch dst.Type {
	case interfaces.TypeInt64:
		if src.Type == interfaces.TypeInt64 {
			dst.Int64s[dstIdx] = src.Int64s[srcIdx]
		} else if src.Type == interfaces.TypeFloat64 {
			dst.Int64s[dstIdx] = int64(src.Float64s[srcIdx])
		}
	case interfaces.TypeFloat64, interfaces.TypeDecimal:
		if src.Type == interfaces.TypeFloat64 || src.Type == interfaces.TypeDecimal {
			dst.Float64s[dstIdx] = src.Float64s[srcIdx]
		} else if src.Type == interfaces.TypeInt64 {
			dst.Float64s[dstIdx] = float64(src.Int64s[srcIdx])
		}
	case interfaces.TypeVarChar, interfaces.TypeDate:
		if len(src.Strings) > srcIdx {
			dst.Strings[dstIdx] = src.Strings[srcIdx]
		}
	case interfaces.TypeBool:
		if len(src.Bools) > srcIdx {
			dst.Bools[dstIdx] = src.Bools[srcIdx]
		}
	}
}

func evaluateUnary(op string, v *interfaces.ColumnVector) (*interfaces.ColumnVector, error) {
	upper := strings.ToUpper(strings.TrimSpace(op))
	length := v.Length
	switch upper {
	case "NOT":
		if v.Type != interfaces.TypeBool {
			return nil, &interfaces.EngineError{
				Code:    interfaces.ErrTypeMismatch,
				Message: "NOT requires boolean operand",
			}
		}
		res := interfaces.ColumnVector{
			Type:     interfaces.TypeBool,
			Length:   length,
			Bools:    make([]bool, length),
			NullMask: make([]bool, length),
		}
		for i := 0; i < length; i++ {
			if v.NullMask[i] {
				res.NullMask[i] = true
			} else {
				res.Bools[i] = !v.Bools[i]
			}
		}
		return &res, nil
	case "IS NULL":
		res := interfaces.ColumnVector{
			Type:     interfaces.TypeBool,
			Length:   length,
			Bools:    make([]bool, length),
			NullMask: make([]bool, length),
		}
		for i := 0; i < length; i++ {
			res.Bools[i] = v.NullMask[i]
		}
		return &res, nil
	case "IS NOT NULL":
		res := interfaces.ColumnVector{
			Type:     interfaces.TypeBool,
			Length:   length,
			Bools:    make([]bool, length),
			NullMask: make([]bool, length),
		}
		for i := 0; i < length; i++ {
			res.Bools[i] = !v.NullMask[i]
		}
		return &res, nil
	case "-":
		if v.Type == interfaces.TypeInt64 {
			res := interfaces.ColumnVector{
				Type:     interfaces.TypeInt64,
				Length:   length,
				Int64s:   make([]int64, length),
				NullMask: make([]bool, length),
			}
			copy(res.NullMask, v.NullMask)
			for i := 0; i < length; i++ {
				if !v.NullMask[i] {
					res.Int64s[i] = -v.Int64s[i]
				}
			}
			return &res, nil
		} else if v.Type == interfaces.TypeFloat64 || v.Type == interfaces.TypeDecimal {
			res := interfaces.ColumnVector{
				Type:     v.Type,
				Length:   length,
				Float64s: make([]float64, length),
				NullMask: make([]bool, length),
			}
			copy(res.NullMask, v.NullMask)
			for i := 0; i < length; i++ {
				if !v.NullMask[i] {
					res.Float64s[i] = -v.Float64s[i]
				}
			}
			return &res, nil
		}
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrTypeMismatch,
			Message: fmt.Sprintf("unary negation not supported on type %d", v.Type),
		}
	default:
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("unsupported unary operator: %s", op),
		}
	}
}

func evaluateBinary(op interfaces.BinaryOp, left, right *interfaces.ColumnVector) (*interfaces.ColumnVector, error) {
	length := left.Length
	if right.Length > length {
		length = right.Length
	}

	switch op {
	case interfaces.OpAnd:
		return evaluateThreeValuedAnd(left, right, length)
	case interfaces.OpOr:
		return evaluateThreeValuedOr(left, right, length)
	case interfaces.OpEq, interfaces.OpNotEq, interfaces.OpLt, interfaces.OpLtEq, interfaces.OpGt, interfaces.OpGtEq:
		return evaluateComparison(op, left, right, length)
	case interfaces.OpAdd, interfaces.OpSub, interfaces.OpMul, interfaces.OpDiv:
		return evaluateArithmetic(op, left, right, length)
	case interfaces.OpLike:
		return evaluateLike(left, right, length)
	default:
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrSyntaxError,
			Message: fmt.Sprintf("unsupported binary operator: %d", op),
		}
	}
}

func evaluateThreeValuedAnd(left, right *interfaces.ColumnVector, length int) (*interfaces.ColumnVector, error) {
	res := interfaces.ColumnVector{
		Type:     interfaces.TypeBool,
		Length:   length,
		Bools:    make([]bool, length),
		NullMask: make([]bool, length),
	}
	for i := 0; i < length; i++ {
		lIdx := i
		if lIdx >= left.Length {
			lIdx = 0
		}
		rIdx := i
		if rIdx >= right.Length {
			rIdx = 0
		}

		lNull := left.NullMask[lIdx]
		rNull := right.NullMask[rIdx]
		lVal := false
		if !lNull && len(left.Bools) > lIdx {
			lVal = left.Bools[lIdx]
		}
		rVal := false
		if !rNull && len(right.Bools) > rIdx {
			rVal = right.Bools[rIdx]
		}

		if (!lNull && !lVal) || (!rNull && !rVal) {
			res.Bools[i] = false
			res.NullMask[i] = false
		} else if lNull || rNull {
			res.NullMask[i] = true
		} else {
			res.Bools[i] = lVal && rVal
			res.NullMask[i] = false
		}
	}
	return &res, nil
}

func evaluateThreeValuedOr(left, right *interfaces.ColumnVector, length int) (*interfaces.ColumnVector, error) {
	res := interfaces.ColumnVector{
		Type:     interfaces.TypeBool,
		Length:   length,
		Bools:    make([]bool, length),
		NullMask: make([]bool, length),
	}
	for i := 0; i < length; i++ {
		lIdx := i
		if lIdx >= left.Length {
			lIdx = 0
		}
		rIdx := i
		if rIdx >= right.Length {
			rIdx = 0
		}

		lNull := left.NullMask[lIdx]
		rNull := right.NullMask[rIdx]
		lVal := false
		if !lNull && len(left.Bools) > lIdx {
			lVal = left.Bools[lIdx]
		}
		rVal := false
		if !rNull && len(right.Bools) > rIdx {
			rVal = right.Bools[rIdx]
		}

		if (!lNull && lVal) || (!rNull && rVal) {
			res.Bools[i] = true
			res.NullMask[i] = false
		} else if lNull || rNull {
			res.NullMask[i] = true
		} else {
			res.Bools[i] = lVal || rVal
			res.NullMask[i] = false
		}
	}
	return &res, nil
}

func evaluateComparison(op interfaces.BinaryOp, left, right *interfaces.ColumnVector, length int) (*interfaces.ColumnVector, error) {
	res := interfaces.ColumnVector{
		Type:     interfaces.TypeBool,
		Length:   length,
		Bools:    make([]bool, length),
		NullMask: make([]bool, length),
	}
	for i := 0; i < length; i++ {
		lIdx := i
		if lIdx >= left.Length {
			lIdx = 0
		}
		rIdx := i
		if rIdx >= right.Length {
			rIdx = 0
		}

		if left.NullMask[lIdx] || right.NullMask[rIdx] {
			res.NullMask[i] = true
			continue
		}

		cmp, err := compareVectorElements(left, lIdx, right, rIdx)
		if err != nil {
			return nil, err
		}
		switch op {
		case interfaces.OpEq:
			res.Bools[i] = (cmp == 0)
		case interfaces.OpNotEq:
			res.Bools[i] = (cmp != 0)
		case interfaces.OpLt:
			res.Bools[i] = (cmp < 0)
		case interfaces.OpLtEq:
			res.Bools[i] = (cmp <= 0)
		case interfaces.OpGt:
			res.Bools[i] = (cmp > 0)
		case interfaces.OpGtEq:
			res.Bools[i] = (cmp >= 0)
		}
	}
	return &res, nil
}

func compareVectorElements(l *interfaces.ColumnVector, lIdx int, r *interfaces.ColumnVector, rIdx int) (int, error) {
	if l.Type == interfaces.TypeInt64 && r.Type == interfaces.TypeInt64 {
		v1 := l.Int64s[lIdx]
		v2 := r.Int64s[rIdx]
		if v1 < v2 {
			return -1, nil
		} else if v1 > v2 {
			return 1, nil
		}
		return 0, nil
	}
	if (l.Type == interfaces.TypeInt64 || l.Type == interfaces.TypeFloat64 || l.Type == interfaces.TypeDecimal) &&
		(r.Type == interfaces.TypeInt64 || r.Type == interfaces.TypeFloat64 || r.Type == interfaces.TypeDecimal) {
		var v1 float64
		if l.Type == interfaces.TypeInt64 {
			v1 = float64(l.Int64s[lIdx])
		} else {
			v1 = l.Float64s[lIdx]
		}
		var v2 float64
		if r.Type == interfaces.TypeInt64 {
			v2 = float64(r.Int64s[rIdx])
		} else {
			v2 = r.Float64s[rIdx]
		}
		if v1 < v2 {
			return -1, nil
		} else if v1 > v2 {
			return 1, nil
		}
		return 0, nil
	}
	if (l.Type == interfaces.TypeVarChar || l.Type == interfaces.TypeDate) &&
		(r.Type == interfaces.TypeVarChar || r.Type == interfaces.TypeDate) {
		v1 := l.Strings[lIdx]
		v2 := r.Strings[rIdx]
		if v1 < v2 {
			return -1, nil
		} else if v1 > v2 {
			return 1, nil
		}
		return 0, nil
	}
	if l.Type == interfaces.TypeBool && r.Type == interfaces.TypeBool {
		v1 := l.Bools[lIdx]
		v2 := r.Bools[rIdx]
		if v1 == v2 {
			return 0, nil
		}
		if !v1 && v2 {
			return -1, nil
		}
		return 1, nil
	}
	return 0, &interfaces.EngineError{
		Code:    interfaces.ErrTypeMismatch,
		Message: fmt.Sprintf("cannot compare type %d with type %d", l.Type, r.Type),
	}
}

func evaluateArithmetic(op interfaces.BinaryOp, left, right *interfaces.ColumnVector, length int) (*interfaces.ColumnVector, error) {
	isFloat := left.Type == interfaces.TypeFloat64 || right.Type == interfaces.TypeFloat64 ||
		left.Type == interfaces.TypeDecimal || right.Type == interfaces.TypeDecimal

	if isFloat {
		res := interfaces.ColumnVector{
			Type:     interfaces.TypeFloat64,
			Length:   length,
			Float64s: make([]float64, length),
			NullMask: make([]bool, length),
		}
		for i := 0; i < length; i++ {
			lIdx := i
			if lIdx >= left.Length {
				lIdx = 0
			}
			rIdx := i
			if rIdx >= right.Length {
				rIdx = 0
			}
			if left.NullMask[lIdx] || right.NullMask[rIdx] {
				res.NullMask[i] = true
				continue
			}
			var v1 float64
			if left.Type == interfaces.TypeInt64 {
				v1 = float64(left.Int64s[lIdx])
			} else {
				v1 = left.Float64s[lIdx]
			}
			var v2 float64
			if right.Type == interfaces.TypeInt64 {
				v2 = float64(right.Int64s[rIdx])
			} else {
				v2 = right.Float64s[rIdx]
			}

			switch op {
			case interfaces.OpAdd:
				res.Float64s[i] = v1 + v2
			case interfaces.OpSub:
				res.Float64s[i] = v1 - v2
			case interfaces.OpMul:
				res.Float64s[i] = v1 * v2
			case interfaces.OpDiv:
				if v2 == 0.0 {
					return nil, &interfaces.EngineError{
						Code:    interfaces.ErrDivisionByZero,
						Message: "division by zero",
					}
				}
				res.Float64s[i] = v1 / v2
			}
		}
		return &res, nil
	}

	if left.Type == interfaces.TypeInt64 && right.Type == interfaces.TypeInt64 {
		res := interfaces.ColumnVector{
			Type:     interfaces.TypeInt64,
			Length:   length,
			Int64s:   make([]int64, length),
			NullMask: make([]bool, length),
		}
		for i := 0; i < length; i++ {
			lIdx := i
			if lIdx >= left.Length {
				lIdx = 0
			}
			rIdx := i
			if rIdx >= right.Length {
				rIdx = 0
			}
			if left.NullMask[lIdx] || right.NullMask[rIdx] {
				res.NullMask[i] = true
				continue
			}
			v1 := left.Int64s[lIdx]
			v2 := right.Int64s[rIdx]
			switch op {
			case interfaces.OpAdd:
				res.Int64s[i] = v1 + v2
			case interfaces.OpSub:
				res.Int64s[i] = v1 - v2
			case interfaces.OpMul:
				res.Int64s[i] = v1 * v2
			case interfaces.OpDiv:
				if v2 == 0 {
					return nil, &interfaces.EngineError{
						Code:    interfaces.ErrDivisionByZero,
						Message: "division by zero",
					}
				}
				res.Int64s[i] = v1 / v2
			}
		}
		return &res, nil
	}

	return nil, &interfaces.EngineError{
		Code:    interfaces.ErrTypeMismatch,
		Message: fmt.Sprintf("arithmetic unsupported between type %d and %d", left.Type, right.Type),
	}
}

func evaluateLike(left, right *interfaces.ColumnVector, length int) (*interfaces.ColumnVector, error) {
	res := interfaces.ColumnVector{
		Type:     interfaces.TypeBool,
		Length:   length,
		Bools:    make([]bool, length),
		NullMask: make([]bool, length),
	}
	for i := 0; i < length; i++ {
		lIdx := i
		if lIdx >= left.Length {
			lIdx = 0
		}
		rIdx := i
		if rIdx >= right.Length {
			rIdx = 0
		}
		if left.NullMask[lIdx] || right.NullMask[rIdx] {
			res.NullMask[i] = true
			continue
		}
		pattern := right.Strings[rIdx]
		str := left.Strings[lIdx]
		res.Bools[i] = matchLike(pattern, str)
	}
	return &res, nil
}

func matchLike(pattern, str string) bool {
	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '%' {
			sb.WriteString(".*")
		} else if c == '_' {
			sb.WriteString(".")
		} else {
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return false
	}
	return re.MatchString(str)
}

func evaluateCast(v *interfaces.ColumnVector, target interfaces.DataType) (*interfaces.ColumnVector, error) {
	res := interfaces.ColumnVector{
		Type:     target,
		Length:   v.Length,
		NullMask: make([]bool, v.Length),
	}
	copy(res.NullMask, v.NullMask)

	switch target {
	case interfaces.TypeInt64:
		res.Int64s = make([]int64, v.Length)
		for i := 0; i < v.Length; i++ {
			if res.NullMask[i] {
				continue
			}
			switch v.Type {
			case interfaces.TypeInt64:
				res.Int64s[i] = v.Int64s[i]
			case interfaces.TypeFloat64, interfaces.TypeDecimal:
				res.Int64s[i] = int64(v.Float64s[i])
			case interfaces.TypeVarChar:
				n, err := strconv.ParseInt(strings.TrimSpace(v.Strings[i]), 10, 64)
				if err != nil {
					return nil, &interfaces.EngineError{
						Code:    interfaces.ErrTypeMismatch,
						Message: fmt.Sprintf("cannot cast '%s' to INT64", v.Strings[i]),
					}
				}
				res.Int64s[i] = n
			case interfaces.TypeBool:
				if v.Bools[i] {
					res.Int64s[i] = 1
				} else {
					res.Int64s[i] = 0
				}
			default:
				return nil, &interfaces.EngineError{
					Code:    interfaces.ErrTypeMismatch,
					Message: fmt.Sprintf("cannot cast type %d to INT64", v.Type),
				}
			}
		}
	case interfaces.TypeFloat64, interfaces.TypeDecimal:
		res.Float64s = make([]float64, v.Length)
		for i := 0; i < v.Length; i++ {
			if res.NullMask[i] {
				continue
			}
			switch v.Type {
			case interfaces.TypeInt64:
				res.Float64s[i] = float64(v.Int64s[i])
			case interfaces.TypeFloat64, interfaces.TypeDecimal:
				res.Float64s[i] = v.Float64s[i]
			case interfaces.TypeVarChar:
				f, err := strconv.ParseFloat(strings.TrimSpace(v.Strings[i]), 64)
				if err != nil {
					return nil, &interfaces.EngineError{
						Code:    interfaces.ErrTypeMismatch,
						Message: fmt.Sprintf("cannot cast '%s' to FLOAT64", v.Strings[i]),
					}
				}
				res.Float64s[i] = f
			default:
				return nil, &interfaces.EngineError{
					Code:    interfaces.ErrTypeMismatch,
					Message: fmt.Sprintf("cannot cast type %d to FLOAT64", v.Type),
				}
			}
		}
	case interfaces.TypeVarChar:
		res.Strings = make([]string, v.Length)
		for i := 0; i < v.Length; i++ {
			if res.NullMask[i] {
				continue
			}
			switch v.Type {
			case interfaces.TypeInt64:
				res.Strings[i] = strconv.FormatInt(v.Int64s[i], 10)
			case interfaces.TypeFloat64, interfaces.TypeDecimal:
				res.Strings[i] = strconv.FormatFloat(v.Float64s[i], 'f', -1, 64)
			case interfaces.TypeVarChar, interfaces.TypeDate:
				res.Strings[i] = v.Strings[i]
			case interfaces.TypeBool:
				res.Strings[i] = strconv.FormatBool(v.Bools[i])
			}
		}
	case interfaces.TypeBool:
		res.Bools = make([]bool, v.Length)
		for i := 0; i < v.Length; i++ {
			if res.NullMask[i] {
				continue
			}
			switch v.Type {
			case interfaces.TypeBool:
				res.Bools[i] = v.Bools[i]
			case interfaces.TypeInt64:
				res.Bools[i] = v.Int64s[i] != 0
			case interfaces.TypeVarChar:
				b, err := strconv.ParseBool(strings.TrimSpace(v.Strings[i]))
				if err != nil {
					return nil, &interfaces.EngineError{
						Code:    interfaces.ErrTypeMismatch,
						Message: fmt.Sprintf("cannot cast '%s' to BOOL", v.Strings[i]),
					}
				}
				res.Bools[i] = b
			default:
				return nil, &interfaces.EngineError{
					Code:    interfaces.ErrTypeMismatch,
					Message: fmt.Sprintf("cannot cast type %d to BOOL", v.Type),
				}
			}
		}
	default:
		return nil, &interfaces.EngineError{
			Code:    interfaces.ErrTypeMismatch,
			Message: fmt.Sprintf("unsupported target cast type: %d", target),
		}
	}
	return &res, nil
}

func toInt64(val any) (int64, bool) {
	switch v := val.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func toFloat64(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	default:
		return 0.0, false
	}
}

func toBool(val any) (bool, bool) {
	switch v := val.(type) {
	case bool:
		return v, true
	case string:
		b, err := strconv.ParseBool(v)
		return b, err == nil
	case int, int64:
		i, _ := toInt64(v)
		return i != 0, true
	default:
		return false, false
	}
}

// CompareValues compares two cell values, handling typed comparison.
func CompareValues(v1 any, v2 any) int {
	if v1 == nil && v2 == nil {
		return 0
	}
	if v1 == nil {
		return -1
	}
	if v2 == nil {
		return 1
	}
	switch a := v1.(type) {
	case int64:
		if b, ok := v2.(int64); ok {
			if a < b {
				return -1
			} else if a > b {
				return 1
			}
			return 0
		}
		if b, ok := v2.(float64); ok {
			if float64(a) < b {
				return -1
			} else if float64(a) > b {
				return 1
			}
			return 0
		}
	case float64:
		var b float64
		switch bb := v2.(type) {
		case float64:
			b = bb
		case int64:
			b = float64(bb)
		default:
			return strings.Compare(fmt.Sprintf("%v", v1), fmt.Sprintf("%v", v2))
		}
		if math.IsNaN(a) && math.IsNaN(b) {
			return 0
		}
		if a < b {
			return -1
		} else if a > b {
			return 1
		}
		return 0
	case string:
		b := fmt.Sprintf("%v", v2)
		return strings.Compare(a, b)
	case bool:
		b, ok := v2.(bool)
		if !ok {
			return 0
		}
		if a == b {
			return 0
		}
		if !a && b {
			return -1
		}
		return 1
	}
	return strings.Compare(fmt.Sprintf("%v", v1), fmt.Sprintf("%v", v2))
}
