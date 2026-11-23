// Package match reconciles statement entries against expected records
// (orders, invoices, boletos, payouts) and explains every decision.
//
// # Passes
//
// Matching runs in four passes. Each pass only sees what earlier passes
// left unmatched, so stronger evidence always wins:
//
//  1. reference: an identifier on the statement (Pix EndToEndId, FITID,
//     boleto nosso número, a document number inside the description)
//     equals the expected Reference or ID, compared after stripping
//     punctuation and leading zeros.
//  2. exact: same amount to the cent, dates within a window, and party
//     names that do not contradict each other.
//  3. group: one entry settling several expected records (an acquirer
//     payout net of fees, a customer paying two orders at once) or one
//     expected record paid by several entries (split payment), found by a
//     bounded subset-sum search.
//  4. fuzzy: amount and date tolerances with a weighted score over
//     amount, date, party similarity and description similarity.
//
// # Assignment
//
// Within the reference, exact and fuzzy passes several candidates may
// compete for the same record. Candidates are split into connected
// components; each component is solved for maximum total score with the
// Hungarian algorithm (O(n³)) when its larger side is at most
// AssignmentConfig.HungarianMaxSize, and greedily otherwise. Candidate
// generation itself is blocked: the exact pass hashes amounts and the
// fuzzy pass binary-searches an amount-sorted index, so the engine never
// builds a full n×m matrix.
//
// # Determinism
//
// Results depend only on the input order and the configuration. The group
// pass is bounded by a node budget per search (deterministic) and by a
// wall-clock budget for the whole pass (a safety net); the summary reports
// how many searches were cut short.
package match
