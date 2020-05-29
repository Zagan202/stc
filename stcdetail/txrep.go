// Internal functions for the stc library.  These functions are
// exported because they may be useful to application code, but
// relegated to a separate package to avoid cluttering the main stc
// documentation.
package stcdetail

import (
	"fmt"
	"github.com/xdrpp/goxdr/xdr"
	"github.com/xdrpp/stc/stx"
	"io"
	"strings"
	"time"
)

// pseudo-selectors
const (
	ps_len     = "len"
	ps_present = "_present"
)

//
// Generating TxRep
//

// Reports illegal values in an XDR structure.
type XdrBadValue []struct {
	Field string
	Msg   string
}

func (e XdrBadValue) Error() string {
	out := &strings.Builder{}
	for i := range e {
		fmt.Fprintf(out, "%s: %s\n", e[i].Field, e[i].Msg)
	}
	return out.String()
}

type trackTypes struct {
	ptrDepth int
	env      *stx.TransactionEnvelope
	err      XdrBadValue
}

func (x *trackTypes) present() string {
	return "." + strings.Repeat("_inner", x.ptrDepth-1) + ps_present
}
func (x *trackTypes) track(i xdr.XdrType) (cleanup func()) {
	oldx := *x
	switch v := i.(type) {
	case xdr.XdrPtr:
		x.ptrDepth++
	case *stx.TransactionEnvelope:
		// In case some XDR structure wraps TransactionEnvelope
		x.env = v
	default:
		return func() {}
	}
	return func() { *x = oldx }
}

type txStringCtx struct {
	accountIDNote func(*stx.AccountID) string
	sigNote       func(*stx.TransactionEnvelope, *stx.DecoratedSignature) string
	signerNote    func(*stx.SignerKey) string
	getHelp       func(string) bool
	out           io.Writer
	native        string
	trackTypes
}

func (xp *txStringCtx) Sprintf(f string, args ...interface{}) string {
	return fmt.Sprintf(f, args...)
}

func (xp *txStringCtx) Marshal_SequenceNumber(name string,
	v *stx.SequenceNumber) {
	fmt.Fprintf(xp.out, "%s: %d\n", name, *v)
}

func (xp *txStringCtx) Marshal_TimePoint(name string, v *stx.TimePoint) {
	fmt.Fprintf(xp.out, "%s: %d%s\n", name, *v, dateComment(*v))
}

var exp10 [20]uint64

func init() {
	val := uint64(1)
	for i := 0; i < len(exp10); i++ {
		exp10[i] = val
		val *= 10
	}
}

// Print a number divided by 10^exp, appending the exponent.
func ScaleFmt(val int64, exp int) string {
	mag := uint64(val)
	if val < 0 {
		mag = uint64(-val)
	}
	unit := exp10[exp]

	out := ""
	for tmag := mag / unit; ; tmag /= 1000 {
		if out != "" {
			out = "," + out
		}
		if tmag < 1000 {
			out = fmt.Sprintf("%d", tmag) + out
			break
		}
		out = fmt.Sprintf("%03d", tmag%1000) + out
	}
	if val < 0 {
		out = "-" + out
	}

	mag %= unit
	if mag > 0 {
		out += strings.TrimRight(fmt.Sprintf(".%0*d", exp, mag), "0")
	}
	return out + "e" + fmt.Sprintf("%d", exp)
}

func dateComment(ut uint64) string {
	it := int64(ut)
	if it <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%s)", time.Unix(it, 0).Format(time.UnixDate))
}

type xdrEnumNames interface {
	fmt.Stringer
	fmt.Scanner
	XdrEnumNames() map[int32]string
}

// Convert an array of bytes into a string of hex digits.  Show an
// empty vector as "0 bytes", since we need to show it as something.
// (Note the bytes is a comment, but just "0" might be unintuitive.)
func PrintVecOpaque(bs []byte) string {
	if len(bs) == 0 {
		return "0 bytes"
	}
	return fmt.Sprintf("%x", bs)
}

func (xp *txStringCtx) Marshal(name string, i xdr.XdrType) {
	defer xp.track(i)()
	defer func() {
		switch v := recover().(type) {
		case nil:
			return
		case xdr.XdrError:
			xp.err = append(xp.err, struct {
				Field string
				Msg   string
			}{ name, v.Error() })
		default:
			panic(v)
		}
	}()
	dot := "."
	if name == "" {
		dot = ""
	}
	if k, ok := i.(xdr.XdrArrayOpaque); ok && len(k) == 32 &&
		strings.HasSuffix(name, "tx.sourceAccountEd25519") {
		name = name[:len(name)-20] + "sourceAccount"
		pk := &stx.AccountID { Type: stx.PUBLIC_KEY_TYPE_ED25519 }
		copy(pk.Ed25519()[:], k)
		i = pk
	}
	switch v := i.(type) {
	case *stx.TransactionEnvelope:
		fmt.Fprintf(xp.out, "%s%stype: %s\n", name, dot, v.Type)
		if ag, ok := v.XdrUnionBody().(xdr.XdrType); ok {
			ag.XdrMarshal(xp, name)
		}
	case *stx.Asset:
		asset := v.String()
		if asset == "native" {
			asset = xp.native
		}
		fmt.Fprintf(xp.out, "%s: %s\n", name, asset)
	case *stx.AccountID:
		ac := v.String()
		if hint := xp.accountIDNote(v); hint != "" {
			fmt.Fprintf(xp.out, "%s: %s (%s)\n", name, ac, hint)
		} else {
			fmt.Fprintf(xp.out, "%s: %s\n", name, ac)
		}
	case *stx.SignerKey:
		if hint := xp.signerNote(v); hint != "" {
			fmt.Fprintf(xp.out, "%s: %s (%s)\n", name, v, hint)
		} else {
			fmt.Fprintf(xp.out, "%s: %s\n", name, v)
		}
	case xdrEnumNames:
		if xp.getHelp(name) {
			fmt.Fprintf(xp.out, "%s: %s (", name, v.String())
			var notfirst bool
			for _, name := range v.XdrEnumNames() {
				if notfirst {
					fmt.Fprintf(xp.out, ", %s", name)
				} else {
					notfirst = true
					fmt.Fprintf(xp.out, "%s", name)
				}
			}
			fmt.Fprintf(xp.out, ")\n")
		} else {
			fmt.Fprintf(xp.out, "%s: %s\n", name, v.String())
		}
	case *xdr.XdrInt64:
		fmt.Fprintf(xp.out, "%s: %s (%s)\n", name, v.String(),
			ScaleFmt(int64(*v), 7))
	case xdr.XdrVecOpaque:
		fmt.Fprintf(xp.out, "%s: %s\n", name, PrintVecOpaque(v.GetByteSlice()))
	case fmt.Stringer:
		fmt.Fprintf(xp.out, "%s: %s\n", name, v.String())
	case xdr.XdrPtr:
		fmt.Fprintf(xp.out, "%s%s: %v\n", name, xp.present(), v.GetPresent())
		v.XdrMarshalValue(xp, name)
	case xdr.XdrVec:
		fmt.Fprintf(xp.out, "%s%s%s: %d\n", name, dot, ps_len, v.GetVecLen())
		v.XdrMarshalN(xp, name, v.GetVecLen())
	case *stx.DecoratedSignature:
		var hint string
		if note := xp.sigNote(xp.env, v); note != "" {
			hint = fmt.Sprintf("%x (%s)", v.Hint, note)
		} else {
			hint = fmt.Sprintf("%x", v.Hint)
		}
		fmt.Fprintf(xp.out, "%[1]s.hint: %[2]s\n%[1]s.signature: %[3]s\n",
			name, hint, PrintVecOpaque(v.Signature))
	case xdr.XdrAggregate:
		v.XdrRecurse(xp, name)
	default:
		fmt.Fprintf(xp.out, "%s: %v\n", name, i)
	}
}

// Writes a human-readable version of a transaction or other XdrType
// structure to out in txrep format.  The following methods on t can
// be used to add comments into the output
//
// Comment for AccountID:
//   AccountIDNote(*AccountID) string
//
// Comment for SignerKey:
//   SignerNote(*SignerKey) string
//
// Comment for Signature:
//   SigNote(*TransactionEnvelope, *DecoratedSignature) string
//
// Help comment for field fieldname:
//   GetHelp(fieldname string) bool
func XdrToTxrep(out io.Writer, name string, t xdr.XdrType) XdrBadValue {
	ctx := txStringCtx{
		accountIDNote: func(*stx.AccountID) string { return "" },
		signerNote: func(*stx.SignerKey) string { return "" },
		sigNote: func(*stx.TransactionEnvelope,
			*stx.DecoratedSignature) string {
			return ""
		},
		getHelp: func(string) bool { return false },
		out:     out,
	}
	ctx.env, _ = t.XdrPointer().(*stx.TransactionEnvelope)

	if i, ok := t.(interface{ AccountIDNote(*stx.AccountID) string }); ok {
		ctx.accountIDNote = i.AccountIDNote
	}
	if i, ok := t.(interface{ SignerNote(*stx.SignerKey) string }); ok {
		ctx.signerNote = i.SignerNote
	}
	if i, ok := t.(interface {
		SigNote(*stx.TransactionEnvelope, *stx.DecoratedSignature) string
	}); ok {
		ctx.sigNote = i.SigNote
	}
	if i, ok := t.(interface{ GetHelp(string) bool }); ok {
		ctx.getHelp = i.GetHelp
	}
	if i, ok := t.(interface{ GetNativeAsset() string }); ok {
		ctx.native = i.GetNativeAsset()
	}
	if ctx.native == "" {
		ctx.native = "native"
	}

	t.XdrMarshal(&ctx, name)
	if len(ctx.err) > 0 {
		return ctx.err
	}
	return nil
}

//
// Parsing TxRep
//

// Represents errors encountered when parsing textual Txrep into XDR
// structures.
type TxrepError []struct {
	Line int
	Msg  string
}

func (e TxrepError) render(prefix string) string {
	out := &strings.Builder{}
	for i := range e {
		fmt.Fprintf(out, "%s%d: %s\n", prefix, e[i].Line, e[i].Msg)
	}
	return out.String()
}

func (e TxrepError) Error() string {
	return e.render("")
}

// Convert TxrepError to string, but placing filename and a colon
// before each line, so as to render messages in the conventional
// "file:line: message" format.
func (e TxrepError) FileError(filename string) string {
	return e.render(filename + ":")
}

func (TxrepError) Is(e error) bool {
	_, ret := e.(TxrepError)
	return ret
}

type lineval struct {
	line int
	val  string
}

type xdrScan struct {
	trackTypes
	kvs     map[string]lineval
	err     TxrepError
	setHelp func(string)
	native  *string
}

func (*xdrScan) Sprintf(f string, args ...interface{}) string {
	return fmt.Sprintf(f, args...)
}

func (xs *xdrScan) report(line int, fmtstr string, args ...interface{}) {
	msg := fmt.Sprintf(fmtstr, args...)
	xs.err = append(xs.err, struct {
		Line int
		Msg  string
	}{line, msg})
}

func (xs *xdrScan) Marshal(name string, i xdr.XdrType) {
	defer xs.track(i)()
	var fixSourceAcct xdr.XdrArrayOpaque
	if k, ok := i.(xdr.XdrArrayOpaque); ok && len(k) == 32 &&
		strings.HasSuffix(name, "tx.sourceAccountEd25519") {
		name = name[:len(name)-20] + "sourceAccount"
		fixSourceAcct = k
		i = &stx.AccountID { Type: stx.PUBLIC_KEY_TYPE_ED25519 }
	}
	lv, ok := xs.kvs[name]
	val := lv.val
	if init, ok := i.(interface{ XdrInitialize() }); ok {
		init.XdrInitialize()
	}
	dot := "."
	if name == "" {
		dot = ""
	}
	switch v := i.(type) {
	case *stx.TransactionEnvelope:
		v.Type.XdrMarshal(xs, name + dot + "type")
		if ag, ok := v.XdrUnionBody().(xdr.XdrType); ok {
			ag.XdrMarshal(xs, name)
		}
	case xdr.XdrArrayOpaque:
		if !ok {
			return
		}
		_, err := fmt.Sscan(val, v)
		if err != nil {
			xs.setHelp(name)
			xs.report(lv.line, "%s", err.Error())
		}
	case xdr.XdrVecOpaque:
		if !ok {
			return
		}
		_, err := fmt.Sscan(val, v)
		if err != nil {
			var word string
			if fmt.Sscanf(val, "%s", &word); word == "0" {
				v.SetByteSlice([]byte{})
			} else {
				xs.setHelp(name)
				xs.report(lv.line, "%s", err.Error())
			}
		} else if len(val) > 0 && val[len(val)-1] == '?' {
			xs.setHelp(name)
		}
	case *xdr.XdrSize:
		var size uint32
		lv = xs.kvs[name+"."+ps_len]
		fmt.Sscan(lv.val, &size)
		if size <= v.XdrBound() {
			v.SetU32(size)
		} else {
			v.SetU32(v.XdrBound())
			xs.report(lv.line, "%s.%s (%d) exceeds maximum size %d.",
				name, ps_len, size, v.XdrBound())
		}
	case fmt.Scanner:
		if !ok {
			return
		}
		_, err := fmt.Sscan(val, v)
		if err != nil {
			xs.setHelp(name)
			xs.report(lv.line, "%s", err.Error())
		}
		if len(val) > 0 && val[len(val)-1] == '?' {
			xs.setHelp(name)
		}
	case xdr.XdrPtr:
		val = "false"
		field := name + xs.present()
		if _, err := fmt.Sscanf(xs.kvs[field].val, "%s", &val); err != nil {
			if ok {
				val = "true"
			} else {
				field = name + "."
				for f := range xs.kvs {
					if strings.HasPrefix(f, field) {
						val = "true"
						break
					}
				}
			}
		}
		switch val {
		case "false":
			v.SetPresent(false)
		case "true":
			v.SetPresent(true)
		default:
			// We are throwing error anyway, so also try parsing any fields
			v.SetPresent(true)
			xs.report(xs.kvs[field].line,
				"%s (%s) must be true or false", field, val)
		}
		v.XdrMarshalValue(xs, name)
	case xdr.XdrAggregate:
		v.XdrRecurse(xs, name)
	default:
		if !ok {
			return
		}
		fmt.Sscan(val, i.XdrPointer())
	}
	delete(xs.kvs, name)
	if fixSourceAcct != nil {
		pk := i.(*stx.AccountID)
		switch pk.Type {
		case stx.PUBLIC_KEY_TYPE_ED25519:
			copy(fixSourceAcct, pk.Ed25519()[:])
		default:
			xs.report(xs.kvs[name].line,
				"sourceAccount must be type Ed25519 for V0 transaction")
		}
	}
}

type inputLine []byte

func (il *inputLine) Scan(ss fmt.ScanState, _ rune) error {
	t, e := ss.Token(false, func(r rune) bool { return r != '\n' })
	*il = inputLine(t)
	return e
}

// Read a line of text without using bufio.
func ReadTextLine(r io.Reader) ([]byte, error) {
	var line inputLine
	var c rune
	fmt.Fscan(r, &line)
	_, err := fmt.Fscanf(r, "%c", &c)
	if err == nil && c != '\n' {
		err = io.EOF
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return []byte(line), err
}

func (xs *xdrScan) readKvs(in io.Reader) {
	xs.kvs = map[string]lineval{}
	lineno := 0
	for {
		bline, err := ReadTextLine(in)
		if err != nil && (err != io.EOF || len(bline) == 0) {
			if err != io.EOF {
				xs.report(lineno, "%s", err.Error())
			}
			return
		}
		lineno++
		line := string(bline)
		if line == "" {
			continue
		}
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			xs.report(lineno, "syntax error")
			continue
		}
		xs.kvs[kv[0]] = lineval{lineno, kv[1]}
	}
}

// Parse input in Txrep format into an XdrType type.  If the XdrType
// has a method named SetHelp(string), then it is called for field
// names when the value ends with '?'.
func XdrFromTxrep(in io.Reader, name string, t xdr.XdrType) TxrepError {
	xs := &xdrScan{}
	if sh, ok := t.(interface{ SetHelp(string) }); ok {
		xs.setHelp = sh.SetHelp
	} else {
		xs.setHelp = func(string) {}
	}
	if nam, ok := t.(interface{ GetNativeAsset() string }); ok {
		na := nam.GetNativeAsset()
		xs.native = &na
	}
	xs.readKvs(in)
	if xs.kvs != nil {
		t.XdrMarshal(xs, name)
	}
	if len(xs.err) != 0 {
		return xs.err
	}
	return nil
}

type xdrExtractor struct {
	target string
	result xdr.XdrType
}

func (*xdrExtractor) Sprintf(f string, args ...interface{}) string {
	return fmt.Sprintf(f, args...)
}

func (xe *xdrExtractor) Marshal(name string, i xdr.XdrType) {
	if xe.result != nil {
		return
	}

	if init, ok := i.(interface{ XdrInitialize() }); ok {
		init.XdrInitialize()
	}

	if name == xe.target {
		xe.result = i
	} else if v, ok := i.(xdr.XdrAggregate); ok {
		v.XdrRecurse(xe, name)
	}
}

// Extract and return a field with a particular txrep name from an XDR
// data structure.  Returns nil if the field name doesn't exist,
// either because it is invalid or because a containing pointer is nil
// or because a union has a different active case.
//
// Note that for pointer fields this functionreturns the pointer, not
// the underlying value.  Hence the XdrPointer() method returns a
// pointer-to-pointer type that is guaranteed not to be nil even if
// the pointer is nil.
func GetTxrepField(t xdr.XdrType, field string) (ret xdr.XdrType) {
	xe := xdrExtractor{ target: field }
	t.XdrMarshal(&xe, "")
	return xe.result
}
