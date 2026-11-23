// Package reconcile is the root of reconcile-go, a dependency-free toolkit
// for bank statement reconciliation.
//
// The work is split across packages:
//
//   - statement parses OFX 1.x/2.x, ISO 20022 camt.052/053/054 and
//     mapped CSV files into a common Entry with signed integer cents.
//   - match runs the multi-pass matching engine (reference, exact,
//     group, fuzzy) with optimal assignment and explained results, and
//     exports them as JSON, CSV or a text report.
//   - dataset generates noisy synthetic datasets with ground truth and
//     scores matcher output (precision, recall, F1).
//
// The reconcile command (cmd/reconcile) wires them together; gen-dataset
// (cmd/gen-dataset) writes synthetic inputs.
package reconcile
