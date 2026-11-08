# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Add SQL lexer and recursive descent parser for SELECT queries, expressions, JOINs, and aggregations.
- feat(storage): implement CSV scanning, batch slicing into typed column vectors, table catalog, and MaxScanBytes limits
- Initial project structure from the factory utility template.
