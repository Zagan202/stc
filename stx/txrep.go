
package stx

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

// Interface for annotating generated Txrep output.
type TxrepAnnotate interface{
	// Returns extra information with which to annotate an account, or
	// "" if no annotation is necessary.
	AccountIDNote(*AccountID) string

	// Returns extra informaiton with which to decorate a signer, or
	// "" if no annotation is necessary.
	SignerNote(*TransactionEnvelope, *DecoratedSignature) string

	// Returns true if field should be rendered with extra help.
	GetHelp(string) bool

	// Called when parsing, rather than generating, Txrep, so as to
	// record that a '?' was found during parsing of a field.
	SetHelp(string)
}

type nullTxrepAnnotate struct{}
func (nullTxrepAnnotate) AccountIDNote(*AccountID) string { return "" }
func (nullTxrepAnnotate) SignerNote(*TransactionEnvelope,
	*DecoratedSignature) string { return "" }
func (nullTxrepAnnotate) SetHelp(string) {}
func (nullTxrepAnnotate) GetHelp(string) bool { return false }

// pseudo-selectors
const (
	ps_len = "len"
	ps_present = "_present"
)

func renderByte(b byte) string {
	if b <= ' ' || b >= '\x7f' {
		return fmt.Sprintf("\\x%02x", b)
	} else if b == '\\' {
		return "\\" + string(b)
	}
	return string(b)
}

func renderCode(bs []byte) string {
	var n int
	for n = len(bs); n > 0 && bs[n-1] == 0; n-- {
	}
	out := &strings.Builder{}
	for i := 0; i < n; i++ {
		out.WriteString(renderByte(bs[i]))
	}
	return out.String()
}

// Slightly convoluted logic to avoid throwing away the account name
// in case the code is bad
func scanCode(out []byte, input string) error {
	ss := strings.NewReader(input)
skipspace:
	if r, _, err := ss.ReadRune(); unicode.IsSpace(r) {
		goto skipspace
	} else if err == nil {
		ss.UnreadRune()
	}
	var i int
	var r = ' '
	var err error
	for i = 0; i < len(out); i++ {
		r, _, err = ss.ReadRune()
		if err == io.EOF || unicode.IsSpace(r) {
			break
		} else if err != nil {
			return err
		} else if r <= ' ' || r >= 127 {
			err = StrKeyError("Invalid character in AssetCode")
			break
		} else if r != '\\' {
			out[i] = byte(r)
			continue
		}
		r, _, err = ss.ReadRune()
		if err != nil {
			return err
		} else if r != 'x' {
			out[i] = byte(r)
		} else if _, err = fmt.Fscanf(ss, "%02x", &out[i]); err != nil {
			return err
		}
	}
	for ; i < len(out); i++ {
		out[i] = 0
	}
	// XXX - might already have read space above
	r, _, err = ss.ReadRune()
	if err != io.EOF && !unicode.IsSpace(r) {
		return StrKeyError("AssetCode too long")
	}
	return nil
}

type trackTypes struct {
	ptrDepth int
	inAsset bool
}

func (x *trackTypes) present() string {
	return "." + strings.Repeat("_inner", x.ptrDepth-1) + ps_present;
}
func (x *trackTypes) track(i XdrType) (cleanup func()) {
	oldx := *x
	switch i.(type) {
	case XdrPtr:
		x.ptrDepth++
	case *Asset:
		x.inAsset = true
	default:
		return func() {}
	}
	return func() { *x = oldx }
}

type txStringCtx struct {
	TxrepAnnotate
	out io.Writer
	env *TransactionEnvelope
	trackTypes
}

func (xp *txStringCtx) Sprintf(f string, args ...interface{}) string {
	return fmt.Sprintf(f, args...)
}

func (xp *txStringCtx) Marshal_SequenceNumber(name string,
	v *SequenceNumber) {
	fmt.Fprintf(xp.out, "%s: %d\n", name, *v)
}

type xdrPointer interface {
	XdrPointer() interface{}
}

var exp10 [20]uint64
func init() {
	val := uint64(1)
	for i := 0; i < len(exp10); i++ {
		exp10[i] = val
		val *= 10
	}
}

func scalePrint(val int64, exp int) string {
	mag := uint64(val)
	if val < 0 { mag = uint64(-val) }
	unit := exp10[exp]

	out := ""
	for tmag := mag/unit;; tmag /= 1000 {
		if out != "" { out = "," + out }
		if tmag < 1000 {
			out = fmt.Sprintf("%d", tmag) + out
			break
		}
		out = fmt.Sprintf("%03d", tmag % 1000) + out
	}
	if val < 0 { out = "-" + out }

	mag %= unit
	if mag > 0 {
		out += strings.TrimRight(fmt.Sprintf(".%0*d", exp, mag), "0")
	}
	return out + "e" + fmt.Sprintf("%d", exp)
}

func dateComment(ut uint64) string {
	it := int64(ut)
	if it <= 0 { return "" }
	return fmt.Sprintf(" (%s)", time.Unix(it, 0).Format(time.UnixDate))
}

type xdrEnumNames interface {
	fmt.Stringer
	fmt.Scanner
	XdrEnumNames() map[int32]string
}

func (xp *txStringCtx) Marshal(name string, i XdrType) {
	defer xp.track(i)()
	switch v := i.(type) {
	case *TimeBounds:
		fmt.Fprintf(xp.out, "%s.minTime: %d%s\n%s.maxTime: %d%s\n",
			name, v.MinTime, dateComment(v.MinTime),
			name, v.MaxTime, dateComment(v.MaxTime))
	case *AccountID:
		ac := v.String()
		if hint := xp.AccountIDNote(v); hint != "" {
			fmt.Fprintf(xp.out, "%s: %s (%s)\n", name, ac, hint)
		} else {
			fmt.Fprintf(xp.out, "%s: %s\n", name, ac)
		}
	case xdrEnumNames:
		if xp.GetHelp(name) {
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
	case XdrArrayOpaque:
		if xp.inAsset {
			fmt.Fprintf(xp.out, "%s: %s\n", name, renderCode(v.GetByteSlice()))
		} else {
			fmt.Fprintf(xp.out, "%s: %s\n", name, v.String())
		}
	case *XdrInt64:
		fmt.Fprintf(xp.out, "%s: %s (%s)\n", name, v.String(),
			scalePrint(int64(*v), 7))
	case fmt.Stringer:
		fmt.Fprintf(xp.out, "%s: %s\n", name, v.String())
	case XdrPtr:
		fmt.Fprintf(xp.out, "%s%s: %v\n", name, xp.present(), v.GetPresent())
		v.XdrMarshalValue(xp, name)
	case XdrVec:
		fmt.Fprintf(xp.out, "%s.%s: %d\n", name, ps_len, v.GetVecLen())
		v.XdrMarshalN(xp, name, v.GetVecLen())
	case *DecoratedSignature:
		var hint string
		if note := xp.SignerNote(xp.env, v); note != "" {
			hint = fmt.Sprintf("%x (%s)", v.Hint, note)
		} else {
			hint = fmt.Sprintf("%x", v.Hint)
		}
		fmt.Fprintf(xp.out, "%[1]s.hint: %[2]s\n%[1]s.signature: %[3]x\n",
			name, hint, v.Signature)
	case XdrAggregate:
		v.XdrMarshal(xp, name)
	default:
		fmt.Fprintf(xp.out, "%s: %v\n", name, i)
	}
}

// Writes a human-readable version of a transaction or other
// XdrAggregate structure to out in txrep format.
func XdrToTxrep(out io.Writer, txe *TransactionEnvelope,
	annotate TxrepAnnotate) {
	ctx := txStringCtx {
		out: out,
		env: txe,
		TxrepAnnotate: annotate,
	}
	if ctx.TxrepAnnotate == nil {
		ctx.TxrepAnnotate = nullTxrepAnnotate{}
	}
	ctx.env.XdrMarshal(&ctx, "")
}




/*
type lineval struct {
	line int
	val string
}

type XdrScan struct {
	kvs map[string]lineval
	help XdrHelp
	err bool
	errmsg strings.Builder
	file string
	trackTypes
}

func (*XdrScan) Sprintf(f string, args ...interface{}) string {
	return fmt.Sprintf(f, args...)
}

func (xs *XdrScan) report(line int, fmtstr string, args...interface{}) {
	xs.err = true
	fmt.Fprintf(&xs.errmsg, "%s:%d: ", xs.file, line)
	fmt.Fprintf(&xs.errmsg, fmtstr, args...)
}

func (xs *XdrScan) Marshal(name string, i XdrType) {
	defer xs.track(i)()
	lv, ok := xs.kvs[name]
	val := lv.val
	switch v := i.(type) {
	case XdrArrayOpaque:
		var err error
		if xs.inAsset {
			err = scanCode(v.GetByteSlice(), val)
		} else if !ok {
			return
		} else {
			_, err = fmt.Sscan(val, v)
		}
		if err != nil {
			xs.help[name] = true
			xs.report(lv.line, "%s\n", err.Error())
		}
	case fmt.Scanner:
		if !ok { return }
		_, err := fmt.Sscan(val, v)
		if err != nil {
			xs.help[name] = true
			xs.report(lv.line, "%s\n", err.Error())
		} else if len(val) > 0 && val[len(val)-1] == '?' {
			xs.help[name] = true
		}
	case XdrPtr:
		val = "false"
		field := name + xs.present()
		fmt.Sscanf(xs.kvs[field].val, "%s", &val)
		switch val {
		case "false":
			v.SetPresent(false)
		case "true":
			v.SetPresent(true)
		default:
			// We are throwing error anyway, so also try parsing any fields
			v.SetPresent(true)
			xs.report(xs.kvs[field].line,
				"%s (%s) must be true or false\n", field, val)
		}
		v.XdrMarshalValue(xs, name)
	case *XdrSize:
		var size uint32
		lv = xs.kvs[name + "." + ps_len]
		fmt.Sscan(lv.val, &size)
		if size <= v.XdrBound() {
			v.SetU32(size)
		} else {
			v.SetU32(v.XdrBound())
			xs.err = true
			xs.report(lv.line, "%s.%s (%d) exceeds maximum size %d.\n",
				name, ps_len, size, v.XdrBound())
		}
	case XdrAggregate:
		v.XdrMarshal(xs, name)
	case xdrPointer:
		if !ok { return }
		fmt.Sscan(val, v.XdrPointer())
	default:
		XdrPanic("XdrScan: Don't know how to parse %s (%T).\n", name, i)
	}
	delete(xs.kvs, name)
}

func TxScan(t XdrAggregate, in string, filename string) (
	help XdrHelp, err error) {
	defer func() {
		if i := recover(); i != nil {
			switch i.(type) {
			case XdrError, StrKeyError:
				err = i.(error)
				//fmt.Fprintln(os.Stderr, err)
				return
			}
			panic(i)
		}
	}()
	kvs := map[string]lineval{}
	help = make(XdrHelp)
	x := XdrScan{kvs: kvs, help: help, file: filename}
	lineno := 0
	for _, line := range strings.Split(in, "\n") {
		lineno++
		if line == "" {
			continue
		}
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			x.report(lineno, "syntax error\n")
			continue
		}
		kvs[kv[0]] = lineval{lineno, kv[1]}
	}
	t.XdrMarshal(&x, "")
	if x.err {
		err = XdrError(x.errmsg.String())
	}
	return
}
*/
