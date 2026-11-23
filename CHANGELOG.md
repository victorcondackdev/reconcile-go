# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/).

## [0.1.0] - 2026-11-22

### Added

- `statement`: parsers for OFX 1.x (SGML) and 2.x (XML), ISO 20022
  camt.052, camt.053 and camt.054, and CSV through a declarative JSON
  mapping (pt-BR amounts and dates, `;` separators, BOM, banner lines,
  credit/debit columns or a D/C indicator, balance-line skipping). Format
  detection by content sniffing. Windows-1252, ISO-8859-1 and ISO-8859-15
  converted to UTF-8 with embedded tables, including mislabelled files.
  OFX dates with offsets such as `20261121120000[-3:BRT]`. Pix EndToEndId
  extraction from free text. Positional `ParseError`s with sentinel errors.
- `match`: four-pass matching engine (reference, exact, group, fuzzy) with
  per-match confidence and feature-level explanations; Hungarian
  assignment per connected component with greedy fallback; bounded
  subset-sum search for many-to-one settlements (acquirer batches net of
  MDR, combined payments) and one-to-many split payments; Jaro-Winkler
  and token similarity with accent folding; duplicate detection; JSON
  configuration layered over defaults; JSON, CSV and text report exports;
  expected-record readers for CSV and JSON.
- `dataset`: deterministic synthetic dataset generator with ground truth
  and noise knobs (dropped references, delays, typos, fees, split and
  combined payments, duplicates, unpaid orders, unexpected entries), file
  writers (CSV, OFX) and precision/recall evaluation.
- `cmd/reconcile`: `run`, `parse`, `config` and `version` commands.
- `cmd/gen-dataset`: dataset writer and precision/recall table.

[0.1.0]: https://github.com/victorcondackdev/reconcile-go/releases/tag/v0.1.0
