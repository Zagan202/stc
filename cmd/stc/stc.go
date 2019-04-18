// Please see the stc.1 man page for complete documentation of this
// command.  The man page is included in the release and available at
// https://xdrpp.github.io/stc/pkg/github.com/xdrpp/stc/cmd/stc/stc.1.html
package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	. "github.com/xdrpp/stc"
	"github.com/xdrpp/stc/stcdetail"
	"github.com/xdrpp/stc/stx"
)

func getAccounts(net *StellarNet, e *TransactionEnvelope, usenet bool) {
	accounts := make(map[stx.AccountID][]HorizonSigner)
	ForEachXdr(e, func(t stx.XdrType)bool {
		if ac, ok := t.(*stx.AccountID); ok {
			if !isZeroAccount(ac) {
				accounts[*ac] = []HorizonSigner{{Key: ac.String()}}
			}
			return true
		}
		return false
	})

	if usenet {
		c := make(chan func())
		for ac := range accounts {
			go func(ac stx.AccountID) {
				if ae, err := net.GetAccountEntry(ac.String()); err == nil {
					c <- func() { accounts[ac] = ae.Signers }
				} else {
					c <- func() {}
				}
			}(ac)
		}
		for i := len(accounts); i > 0; i-- {
			(<-c)()
		}
	}

	for ac, signers := range accounts {
		acs := ac.String()
		for _, signer := range signers {
			var comment string
			if acs != signer.Key {
				comment = fmt.Sprintf("signer for account %s", acs)
			}
			net.Signers.Add(signer.Key, comment)
		}
	}
}

func doKeyGen(outfile string) {
	sk := NewPrivateKey(stx.PUBLIC_KEY_TYPE_ED25519)
	if outfile == "" {
		fmt.Println(sk)
		fmt.Println(sk.Public())
		// fmt.Printf("%x\n", sk.Public().Hint())
	} else {
		if FileExists(outfile) {
			fmt.Fprintf(os.Stderr, "%s: file already exists\n", outfile)
			return
		}
		bytePassword := stcdetail.GetPass2("Passphrase: ")
		if FileExists(outfile) {
			fmt.Fprintf(os.Stderr, "%s: file already exists\n", outfile)
			return
		}
		err := sk.Save(outfile, bytePassword)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
		} else {
			fmt.Println(sk.Public())
			//fmt.Printf("%x\n", sk.Public().Hint())
		}
	}
}

func getSecKey(file string) (*PrivateKey, error) {
	var sk *PrivateKey
	var err error
	if file == "" {
		sk, err = InputPrivateKey("Secret key: ")
	} else {
		sk, err = LoadPrivateKey(file)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
	}
	return sk, err
}

func doSec2pub(file string) {
	sk, _ := getSecKey(file)
	if sk != nil {
		fmt.Println(sk.Public().String())
	}
}

var u256zero stx.Uint256
func isZeroAccount(ac *stx.AccountID) bool {
	return ac.Type == stx.PUBLIC_KEY_TYPE_ED25519 &&
		bytes.Compare(ac.Ed25519()[:], u256zero[:]) == 0
}

func fixTx(net *StellarNet, e *TransactionEnvelope) {
	var async stcdetail.Async
	async.RunVoid(func(){
		if h, err := net.GetFeeStats(); err == nil {
			// 20 should be a parameter
			e.Tx.Fee = h.Percentile(20) * uint32(len(e.Tx.Operations))
		}
	})
	if !isZeroAccount(&e.Tx.SourceAccount) {
		async.RunVoid(func(){
			if a, _ := net.GetAccountEntry(e.Tx.SourceAccount.String());
			a != nil {
				e.Tx.SeqNum = a.NextSeq()
			}
		})
	}
	async.Wait()
}

// Guess whether input is key: value lines or compiled base64
func isCompiled(content string) bool {
	if len(content) != 0 && strings.IndexByte(content, ':') == -1 {
		bs, err := base64.StdEncoding.DecodeString(content)
		if err == nil && len(bs) > 0 {
			return true
		}
	}
	return false
}

type ParseError struct {
	stcdetail.TxrepError
	Filename string
}

func (pe ParseError) Error() string {
	return pe.FileError(pe.Filename)
}

func readTx(infile string) (
	txe *TransactionEnvelope, compiled bool, err error) {
	var input []byte
	if infile == "-" {
		input, err = ioutil.ReadAll(os.Stdin)
		infile = "(stdin)"
	} else {
		input, err = ioutil.ReadFile(infile)
	}
	if err != nil {
		return
	}
	sinput := string(input)

	if isCompiled(sinput) {
		compiled = true
		txe, err = TxFromBase64(sinput)
	} else if newe, pe := TxFromRep(sinput); pe != nil {
		err = ParseError{pe, infile}
	} else {
		txe = newe
	}
	return
}

func mustReadTx(infile string) (*TransactionEnvelope, bool) {
	e, compiled, err := readTx(infile)
	if err != nil {
		fmt.Println(os.Stderr, err)
		os.Exit(1)
	}
	return e, compiled
}

func writeTx(outfile string, e *TransactionEnvelope, net *StellarNet,
	compiled bool) error {
	var output string
	if compiled {
		output = TxToBase64(e) + "\n"
	} else {
		output = net.TxToRep(e)
	}

	if outfile == "" {
		fmt.Print(output)
	} else {
		if err := stcdetail.SafeWriteFile(outfile, output, 0666); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return err
		}
	}
	return nil
}

func mustWriteTx(outfile string, e *TransactionEnvelope, net *StellarNet,
	compiled bool) {
	if err := writeTx(outfile, e, net, compiled); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func signTx(net *StellarNet, key string, e *TransactionEnvelope) error {
	if key != "" {
		key = AdjustKeyName(key)
	}
	sk, err := getSecKey(key)
	if err != nil {
		return err
	}
	net.Signers.Add(sk.Public().String(), "")
	if err = net.SignTx(sk, e); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	return nil
}

func editor(args ...string) {
	ed, ok := os.LookupEnv("EDITOR")
	if !ok {
		ed = "vi"
	}
	if path, err := exec.LookPath(ed); err == nil {
		ed = path
	}

	argv := append([]string{ed}, args...)
	proc, err := os.StartProcess(ed, argv, &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	proc.Wait()
}

func firstDifferentLine(a []byte, b []byte) (lineno int) {
	n := len(a)
	m := n
	if n > len(b) {
		n = len(b)
	} else {
		m = n
	}
	lineno = 1
	for i := 0; ; i++ {
		if i >= n {
			if i >= m {
				lineno = 0
			}
			break
		}
		if a[i] != b[i] {
			break
		}
		if a[i] == '\n' {
			lineno++
		}
	}
	return
}

func doEdit(net *StellarNet, arg string) {
	if arg == "" || arg == "-" {
		fmt.Fprintln(os.Stderr, "Must supply file name to edit")
		os.Exit(1)
	}

	e, compiled, err := readTx(arg)
	if os.IsNotExist(err) {
		e = NewTransactionEnvelope()
		compiled = true
	} else if err != nil {
		fmt.Fprint(os.Stderr, err.Error())
		os.Exit(1)
	}
	getAccounts(net, e, false)

	f, err := ioutil.TempFile("", progname)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path + "~")
	defer os.Remove(path)

	var contents, lastcontents []byte
	for {
		if err == nil {
			lastcontents = []byte(net.TxToRep(e))
			ioutil.WriteFile(path, lastcontents, 0600)
		}

		fi1, staterr := os.Stat(path)
		if staterr != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}

		line := firstDifferentLine(contents, lastcontents)
		if err != nil {
			fmt.Fprint(os.Stderr, err.Error())
			fmt.Printf("Press return to run editor.")
			stcdetail.ReadTextLine(os.Stdin)
			if pe, ok := err.(ParseError); ok {
				line = pe.TxrepError[0].Line
			}
		}
		editor(fmt.Sprintf("+%d", line), path)

		if err == nil {
			fi2, staterr := os.Stat(path)
			if staterr != nil {
				fmt.Println(err.Error())
				os.Exit(1)
			}
			if fi1.Size() == fi2.Size() && fi1.ModTime() == fi2.ModTime() {
				break
			}
		}

		contents, err = ioutil.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		err = nil
		if newe, pe := TxFromRep(string(contents)); pe != nil {
			err = ParseError{pe, path}
		} else {
			e = newe
		}
	}

	mustWriteTx(arg, e, net, compiled)
}

func b2i(bs ...bool) int {
	ret := 0
	for _, b := range bs {
		if b {
			ret++
		}
	}
	return ret
}

var progname string

func main() {
	opt_compile := flag.Bool("c", false, "Compile output to base64 XDR")
	opt_keygen := flag.Bool("keygen", false, "Create a new signing keypair")
	opt_sec2pub := flag.Bool("sec2pub", false, "Get public key from private")
	opt_output := flag.String("o", "", "Output to `FILE` instead of stdout")
	opt_preauth := flag.Bool("preauth", false,
		"Hash transaction to strkey for use as a pre-auth transaction signer")
	opt_txhash := flag.Bool("txhash", false, "Hash transaction to hex format")
	opt_inplace := flag.Bool("i", false, "Edit the input file in place")
	opt_sign := flag.Bool("sign", false, "Sign the transaction")
	opt_key := flag.String("key", "", "Use secret signing key in `FILE`")
	opt_netname := flag.String("net", "",
		"Use Network `NET` (e.g., test); default: $STCNET, otherwise main")
	opt_update := flag.Bool("u", false,
		"Query network to update fee and sequence number")
	opt_learn := flag.Bool("l", false, "Learn new signers")
	opt_help := flag.Bool("help", false, "Print usage information")
	opt_post := flag.Bool("post", false,
		"Post transaction instead of editing it")
	opt_nopass := flag.Bool("nopass", false, "Never prompt for passwords")
	opt_edit := flag.Bool("edit", false,
		"keep editing the file until it doesn't change")
	opt_import_key := flag.Bool("import-key", false,
		"Import signing key to your $STCDIR directory")
	opt_export_key := flag.Bool("export-key", false,
		"Export signing key from your $STCDIR directory")
	opt_list_keys := flag.Bool("list-keys", false,
		"List keys that have been stored in $STCDIR")
	opt_fee_stats := flag.Bool("fee-stats", false,
		"Dump fee stats from network")
	opt_acctinfo := flag.Bool("qa", false,
		"Query Horizon for information on account")
	opt_friendbot := flag.Bool("create", false,
		"Create and fund account (on testnet only)")
	if pos := strings.LastIndexByte(os.Args[0], '/'); pos >= 0 {
		progname = os.Args[0][pos+1:]
	} else {
		progname = os.Args[0]
	}
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
`Usage: %[1]s [-net=ID] [-sign] [-c] [-l] [-u] [-i | -o FILE] INPUT-FILE
       %[1]s -edit [-net=ID] FILE
       %[1]s -post [-net=ID] INPUT-FILE
       %[1]s -preauth [-net=ID] INPUT-FILE
       %[1]s -txhash [-net=ID] _INPUT-FILE
       %[1]s -fee-stats
       %[1]s -qa [-net=ID] ACCT
       %[1]s -create [-net=ID] ACCT
       %[1]s -keygen [NAME]
       %[1]s -sec2pub [NAME]
       %[1]s -import-key NAME
       %[1]s -export-key NAME
       %[1]s -list-keys
`, progname)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *opt_help {
		flag.CommandLine.SetOutput(os.Stdout)
		flag.Usage()
		return
	}

	if n := b2i(*opt_preauth, *opt_txhash, *opt_post, *opt_edit, *opt_keygen,
		*opt_sec2pub, *opt_import_key, *opt_export_key,
		*opt_acctinfo, *opt_friendbot,
		*opt_list_keys, *opt_fee_stats); n > 1 || len(flag.Args()) > 1 ||
		(len(flag.Args()) == 0 &&
			!(*opt_keygen || *opt_sec2pub || *opt_list_keys ||
			*opt_fee_stats || *opt_acctinfo || *opt_friendbot)) {
		flag.Usage()
		os.Exit(2)
	} else if n == 1 {
		bail := false
		if *opt_sign || *opt_key != "" {
			fmt.Fprintln(os.Stderr,
				"--sign and --key only availble in default mode")
			bail = true
		}
		if *opt_learn || *opt_update {
			fmt.Fprintln(os.Stderr, "-l and -u only availble in default mode")
			bail = true
		}
		if *opt_inplace || *opt_output != "" {
			fmt.Fprintln(os.Stderr, "-i and -o only availble in default mode")
			bail = true
		}
		if *opt_compile {
			fmt.Fprintln(os.Stderr, "-c o only availble in default mode")
			bail = true
		}
		if bail {
			os.Exit(2)
		}
	}

	var arg string
	if len(flag.Args()) == 1 {
		arg = flag.Args()[0]
	}

	if *opt_nopass {
		stcdetail.PassphraseFile = io.MultiReader()
	} else if arg == "-" {
		stcdetail.PassphraseFile = nil
	}

	switch {
	case *opt_keygen:
		if arg != "" {
			arg = AdjustKeyName(arg)
		}
		doKeyGen(arg)
		return
	case *opt_sec2pub:
		if arg != "" {
			arg = AdjustKeyName(arg)
		}
		doSec2pub(arg)
		return
	case *opt_import_key:
		arg = AdjustKeyName(arg)
		sk, err := InputPrivateKey("Secret key: ")
		if err == nil {
			err = sk.Save(arg, stcdetail.GetPass2("Passphrase: "))
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	case *opt_export_key:
		arg = AdjustKeyName(arg)
		sk, err := LoadPrivateKey(arg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		fmt.Println(sk)
		return
	case *opt_list_keys:
		for _, k := range GetKeyNames() {
			fmt.Println(k)
		}
		return
	}

	if *opt_netname == "" {
		*opt_netname = os.Getenv("STCNET")
	}
	if *opt_netname == "" {
		*opt_netname = "default"
	}
	net := GetStellarNet(*opt_netname)
	if net == nil {
		fmt.Fprintf(os.Stderr, "unknown network %q\n", *opt_netname)
		os.Exit(1)
	}

	if *opt_acctinfo {
		var acct AccountID
		if _, err := fmt.Sscan(arg, &acct); err != nil {
			fmt.Fprintln(os.Stderr, "syntactically invalid account")
			os.Exit(1)
		}
		if ae, err := net.GetAccountEntry(arg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		} else {
			fmt.Print(ae)
		}
		return
	}

	if *opt_friendbot {
		var acct AccountID
		if _, err := fmt.Sscan(arg, &acct); err != nil {
			fmt.Fprintln(os.Stderr, "syntactically invalid account")
			os.Exit(1)
		}
		if _, err := net.Get("friendbot?addr=" + arg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *opt_fee_stats {
		fs, err := net.GetFeeStats()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error fetching fee stats: %s\n",
				err.Error())
			os.Exit(1)
		}
		fmt.Print(fs)
		return
	}

	if *opt_edit {
		doEdit(net, arg)
		return
	}

	e, _ := mustReadTx(arg)
	switch {
	case *opt_post:
		res, err := net.Post(e)
		if err == nil {
			fmt.Print(stx.XdrToString(res))
		} else {
			fmt.Fprintf(os.Stderr, "Post transaction failed: %s\n", err)
			os.Exit(1)
		}
	case *opt_txhash:
		fmt.Printf("%x\n", net.HashTx(e))
	case *opt_preauth:
		sk := stx.SignerKey{Type: stx.SIGNER_KEY_TYPE_PRE_AUTH_TX}
		*sk.PreAuthTx() = *net.HashTx(e)
		fmt.Println(&sk)
	default:
		getAccounts(net, e, *opt_learn)
		if *opt_update {
			fixTx(net, e)
		}
		if *opt_sign || *opt_key != "" {
			if err := signTx(net, *opt_key, e); err != nil {
				os.Exit(1)
			}
		}
		if *opt_learn {
			SaveSigners(net)
		}
		if *opt_inplace {
			*opt_output = arg
		}
		mustWriteTx(*opt_output, e, net, *opt_compile)
	}
}
