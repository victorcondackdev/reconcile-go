package statement

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/victorcondackdev/reconcile-go/internal/charset"
	"github.com/victorcondackdev/reconcile-go/internal/decimal"
)

// ParseCAMT reads an ISO 20022 cash management message: camt.053
// (BankToCustomerStatement), camt.052 (BankToCustomerAccountReport) or
// camt.054 (BankToCustomerDebitCreditNotification). Any message version is
// accepted because elements are matched by local name, ignoring the
// namespace.
//
// Each Ntry becomes one Entry signed by CdtDbtInd. When the entry has a
// single TxDtls, its references (EndToEndId, creditor reference), related
// party and remittance information are lifted into the Entry. With
// Options.SplitBatches, an Ntry with several TxDtls that carry their own
// amounts yields one Entry per transaction.
func ParseCAMT(r io.Reader) (*Statement, error) {
	return ParseWithOptions(r, Options{Format: "camt"})
}

type camtDocument struct {
	XMLName xml.Name
	Stmt    []camtStatement `xml:"BkToCstmrStmt>Stmt"`
	Rpt     []camtStatement `xml:"BkToCstmrAcctRpt>Rpt"`
	Ntfctn  []camtStatement `xml:"BkToCstmrDbtCdtNtfctn>Ntfctn"`
}

type camtStatement struct {
	ID     string `xml:"Id"`
	FrToDt struct {
		FrDtTm string `xml:"FrDtTm"`
		ToDtTm string `xml:"ToDtTm"`
	} `xml:"FrToDt"`
	Acct struct {
		ID struct {
			IBAN string `xml:"IBAN"`
			Othr struct {
				ID string `xml:"Id"`
			} `xml:"Othr"`
		} `xml:"Id"`
		Ccy  string `xml:"Ccy"`
		Svcr struct {
			FinInstnID struct {
				BICFI       string `xml:"BICFI"`
				BIC         string `xml:"BIC"`
				ClrSysMmbID struct {
					MmbID string `xml:"MmbId"`
				} `xml:"ClrSysMmbId"`
			} `xml:"FinInstnId"`
		} `xml:"Svcr"`
	} `xml:"Acct"`
	Bal  []camtBalance `xml:"Bal"`
	Ntry []camtEntry   `xml:"Ntry"`
}

type camtAmount struct {
	Value string `xml:",chardata"`
	Ccy   string `xml:"Ccy,attr"`
}

type camtDate struct {
	Dt   string `xml:"Dt"`
	DtTm string `xml:"DtTm"`
}

type camtBalance struct {
	Tp struct {
		CdOrPrtry struct {
			Cd    string `xml:"Cd"`
			Prtry string `xml:"Prtry"`
		} `xml:"CdOrPrtry"`
	} `xml:"Tp"`
	Amt       camtAmount `xml:"Amt"`
	CdtDbtInd string     `xml:"CdtDbtInd"`
	Dt        camtDate   `xml:"Dt"`
}

type camtEntry struct {
	NtryRef     string     `xml:"NtryRef"`
	Amt         camtAmount `xml:"Amt"`
	CdtDbtInd   string     `xml:"CdtDbtInd"`
	RvslInd     string     `xml:"RvslInd"`
	Sts         camtStatus `xml:"Sts"`
	BookgDt     camtDate   `xml:"BookgDt"`
	ValDt       camtDate   `xml:"ValDt"`
	AcctSvcrRef string     `xml:"AcctSvcrRef"`
	BkTxCd      struct {
		Domn struct {
			Cd   string `xml:"Cd"`
			Fmly struct {
				Cd        string `xml:"Cd"`
				SubFmlyCd string `xml:"SubFmlyCd"`
			} `xml:"Fmly"`
		} `xml:"Domn"`
		Prtry struct {
			Cd string `xml:"Cd"`
		} `xml:"Prtry"`
	} `xml:"BkTxCd"`
	NtryDtls []struct {
		TxDtls []camtTx `xml:"TxDtls"`
	} `xml:"NtryDtls"`
	AddtlNtryInf string `xml:"AddtlNtryInf"`
}

// camtStatus covers both <Sts>BOOK</Sts> (camt.053 up to .07) and
// <Sts><Cd>BOOK</Cd></Sts> (version .08 onwards).
type camtStatus struct {
	Value string `xml:",chardata"`
	Cd    string `xml:"Cd"`
	Prtry string `xml:"Prtry"`
}

func (s camtStatus) String() string {
	return strings.TrimSpace(firstNonEmpty(s.Cd, s.Prtry, strings.TrimSpace(s.Value)))
}

type camtTx struct {
	Refs struct {
		MsgID       string `xml:"MsgId"`
		AcctSvcrRef string `xml:"AcctSvcrRef"`
		PmtInfID    string `xml:"PmtInfId"`
		InstrID     string `xml:"InstrId"`
		EndToEndID  string `xml:"EndToEndId"`
		TxID        string `xml:"TxId"`
		UETR        string `xml:"UETR"`
	} `xml:"Refs"`
	Amt     camtAmount `xml:"Amt"`
	AmtDtls struct {
		TxAmt struct {
			Amt camtAmount `xml:"Amt"`
		} `xml:"TxAmt"`
	} `xml:"AmtDtls"`
	CdtDbtInd string `xml:"CdtDbtInd"`
	RltdPties struct {
		Dbtr      camtParty `xml:"Dbtr"`
		Cdtr      camtParty `xml:"Cdtr"`
		UltmtDbtr camtParty `xml:"UltmtDbtr"`
		UltmtCdtr camtParty `xml:"UltmtCdtr"`
	} `xml:"RltdPties"`
	RmtInf struct {
		Ustrd []string `xml:"Ustrd"`
		Strd  []struct {
			CdtrRefInf struct {
				Ref string `xml:"Ref"`
			} `xml:"CdtrRefInf"`
		} `xml:"Strd"`
	} `xml:"RmtInf"`
	AddtlTxInf string `xml:"AddtlTxInf"`
}

// camtParty covers <Dbtr><Nm/></Dbtr> (up to .07) and
// <Dbtr><Pty><Nm/></Pty></Dbtr> (.08 onwards).
type camtParty struct {
	Nm  string `xml:"Nm"`
	Pty struct {
		Nm string `xml:"Nm"`
	} `xml:"Pty"`
}

func (p camtParty) name() string { return strings.TrimSpace(firstNonEmpty(p.Nm, p.Pty.Nm)) }

func parseCAMT(data []byte, opts Options) (*Statement, error) {
	format := Detect(data)
	switch format {
	case FormatCAMT052, FormatCAMT053, FormatCAMT054:
	default:
		format = FormatCAMT053
	}
	dec := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.CharsetReader = charset.NewReader
	var doc camtDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, &ParseError{Format: format, Err: fmt.Errorf("%w: %v", ErrSyntax, err)}
	}
	if doc.XMLName.Local != "Document" {
		return nil, &ParseError{Format: format, Err: fmt.Errorf("%w: root element is <%s>, want <Document>", ErrSyntax, doc.XMLName.Local)}
	}
	stmts := append(append(doc.Stmt, doc.Rpt...), doc.Ntfctn...)
	if len(stmts) == 0 {
		return nil, &ParseError{Format: format, Err: fmt.Errorf("%w: no Stmt, Rpt or Ntfctn element", ErrSyntax)}
	}
	st := &Statement{Format: format}
	n := 0
	for si, s := range stmts {
		if st.AccountID == "" {
			st.AccountID = firstNonEmpty(s.Acct.ID.IBAN, s.Acct.ID.Othr.ID)
			st.BankID = firstNonEmpty(s.Acct.Svcr.FinInstnID.BICFI, s.Acct.Svcr.FinInstnID.BIC, s.Acct.Svcr.FinInstnID.ClrSysMmbID.MmbID)
			st.Currency = strings.ToUpper(s.Acct.Ccy)
		}
		if t, err := parseISODateTime(s.FrToDt.FrDtTm); err == nil && (st.Start.IsZero() || t.Before(st.Start)) {
			st.Start = t
		}
		if t, err := parseISODateTime(s.FrToDt.ToDtTm); err == nil && t.After(st.End) {
			st.End = t
		}
		for _, b := range s.Bal {
			amt, err := camtSigned(b.Amt.Value, b.CdtDbtInd)
			if err != nil {
				return nil, &ParseError{Format: format, Field: "Bal/Amt", Value: b.Amt.Value, Err: err}
			}
			d, _ := b.Dt.time()
			st.Balances = append(st.Balances, Balance{
				Kind:        firstNonEmpty(b.Tp.CdOrPrtry.Cd, b.Tp.CdOrPrtry.Prtry),
				AmountCents: amt, Currency: strings.ToUpper(firstNonEmpty(b.Amt.Ccy, st.Currency)), Date: d,
			})
		}
		for ni, e := range s.Ntry {
			n++
			es, err := camtEntries(e, si, ni, n, format, st.Currency, opts)
			if err != nil {
				return nil, err
			}
			st.Entries = append(st.Entries, es...)
		}
	}
	return st, nil
}

func camtSigned(v, ind string) (int64, error) {
	amt, err := decimal.Parse(v, decimal.Options{Decimal: '.'})
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrAmount, err)
	}
	if amt < 0 {
		return 0, fmt.Errorf("%w: negative amount; the sign belongs in CdtDbtInd", ErrAmount)
	}
	switch strings.TrimSpace(ind) {
	case "CRDT":
		return amt, nil
	case "DBIT":
		return -amt, nil
	}
	return 0, fmt.Errorf("%w: CdtDbtInd %q", ErrMissing, ind)
}

func (d camtDate) time() (time.Time, error) {
	if s := strings.TrimSpace(d.DtTm); s != "" {
		return parseISODateTime(s)
	}
	return parseISODateTime(d.Dt)
}

var isoLayouts = []string{
	time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05",
	"2006-01-02Z07:00", "2006-01-02",
}

func parseISODateTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range isoLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: %q", ErrDate, s)
}

func camtEntries(e camtEntry, si, ni, n int, format Format, stCur string, opts Options) ([]Entry, error) {
	fail := func(field, value string, err error) error {
		return &ParseError{Format: format, Index: n, Field: field, Value: value, Err: err}
	}
	amt, err := camtSigned(e.Amt.Value, e.CdtDbtInd)
	if err != nil {
		return nil, fail("Amt", e.Amt.Value, err)
	}
	bd, err := e.BookgDt.time()
	if err != nil {
		if strings.TrimSpace(e.BookgDt.Dt+e.BookgDt.DtTm) != "" {
			return nil, fail("BookgDt", e.BookgDt.Dt+e.BookgDt.DtTm, err)
		}
		// camt.054 notifications of pending items may omit BookgDt.
		if bd, err = e.ValDt.time(); err != nil {
			return nil, fail("BookgDt", "", ErrMissing)
		}
	}
	vd, err := e.ValDt.time()
	if err != nil {
		vd = bd
	}
	cur := strings.ToUpper(firstNonEmpty(e.Amt.Ccy, stCur, "BRL"))
	var txs []camtTx
	for _, d := range e.NtryDtls {
		txs = append(txs, d.TxDtls...)
	}
	id := firstNonEmpty(strings.TrimSpace(e.AcctSvcrRef), strings.TrimSpace(e.NtryRef))
	if id == "" {
		id = "camt-" + strconv.Itoa(si+1) + "-" + strconv.Itoa(ni+1)
	}
	typ := strings.Trim(e.BkTxCd.Domn.Cd+"/"+e.BkTxCd.Domn.Fmly.Cd+"/"+e.BkTxCd.Domn.Fmly.SubFmlyCd, "/")
	if typ == "" {
		typ = e.BkTxCd.Prtry.Cd
	}
	raw := map[string]string{
		"NtryRef": e.NtryRef, "AcctSvcrRef": e.AcctSvcrRef, "Sts": e.Sts.String(),
		"CdtDbtInd": e.CdtDbtInd, "Amt": e.Amt.Value, "TxDtls": strconv.Itoa(len(txs)),
	}
	if strings.EqualFold(strings.TrimSpace(e.RvslInd), "true") {
		raw["RvslInd"] = "true"
	}
	base := Entry{
		ID: id, BookingDate: bd, ValueDate: vd, AmountCents: amt, Currency: cur,
		Description: strings.TrimSpace(e.AddtlNtryInf), Type: typ, Raw: raw,
	}

	if opts.SplitBatches && len(txs) > 1 {
		out := make([]Entry, 0, len(txs))
		var sum int64
		for ti, tx := range txs {
			a := firstNonEmpty(tx.Amt.Value, tx.AmtDtls.TxAmt.Amt.Value)
			ind := firstNonEmpty(tx.CdtDbtInd, e.CdtDbtInd)
			if a == "" {
				return []Entry{base}, nil
			}
			v, err := camtSigned(a, ind)
			if err != nil {
				return nil, fail("TxDtls/Amt", a, err)
			}
			sum += v
			x := base
			x.ID = id + "/" + strconv.Itoa(ti+1)
			x.AmountCents = v
			x.Raw = map[string]string{}
			for k, val := range raw {
				x.Raw[k] = val
			}
			applyTx(&x, tx, ind)
			out = append(out, x)
		}
		if sum != amt {
			return nil, fail("TxDtls/Amt", strconv.FormatInt(sum, 10), fmt.Errorf("%w: transaction amounts do not add up to the entry amount", ErrAmount))
		}
		return out, nil
	}
	if len(txs) == 1 {
		applyTx(&base, txs[0], e.CdtDbtInd)
	} else if len(txs) > 1 {
		var e2e []string
		for _, tx := range txs {
			if r := cleanRef(tx.Refs.EndToEndID); r != "" {
				e2e = append(e2e, r)
			}
		}
		raw["EndToEndIds"] = strings.Join(e2e, ",")
	}
	return []Entry{base}, nil
}

func cleanRef(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NOTPROVIDED") {
		return ""
	}
	return s
}

func applyTx(x *Entry, tx camtTx, ind string) {
	ref := firstNonEmpty(cleanRef(tx.Refs.EndToEndID), "")
	for _, s := range tx.RmtInf.Strd {
		if ref == "" {
			ref = strings.TrimSpace(s.CdtrRefInf.Ref)
		}
	}
	if ref == "" {
		ref = firstNonEmpty(cleanRef(tx.Refs.TxID), cleanRef(tx.Refs.InstrID))
	}
	x.Reference = ref
	if strings.TrimSpace(ind) == "DBIT" {
		x.Counterparty = firstNonEmpty(tx.RltdPties.Cdtr.name(), tx.RltdPties.UltmtCdtr.name())
	} else {
		x.Counterparty = firstNonEmpty(tx.RltdPties.Dbtr.name(), tx.RltdPties.UltmtDbtr.name())
	}
	x.Description = joinNonEmpty(" ", x.Description, strings.Join(tx.RmtInf.Ustrd, " "), tx.AddtlTxInf)
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			x.Raw[k] = v
		}
	}
	set("EndToEndId", tx.Refs.EndToEndID)
	set("TxId", tx.Refs.TxID)
	set("InstrId", tx.Refs.InstrID)
	set("MsgId", tx.Refs.MsgID)
	set("UETR", tx.Refs.UETR)
	set("Dbtr", tx.RltdPties.Dbtr.name())
	set("Cdtr", tx.RltdPties.Cdtr.name())
}
