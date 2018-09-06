% goxdr(1)
% David Mazieres
%

# NAME

goxdr - Go XDR compiler

# SYNOPSIS

goxdr [-b] [-o _output.go_] [-p _package_] [_file1.x_ [_file2.x_ ...]]

# DESCRIPTION

goxdr compiles an RFC4506 XDR interface file to a set of go data
structures that can be either marshaled to standard XDR binary format
or traversed for other purposes such as pretty-pringing.  It does not
rely on go's reflection facilities, and so can be used to special-case
handling of XDR typedefs that yield identical go types.

goxdr-compiled XDR types map to the most intuitive go data structures.
In particular, strings map to strings, pointers map to pointers,
fixed-size arrays map to arrays, and variable-length arrays map to
slices, without new type declarations would complicating assignment.
E.g., the XDR `typedef string mystring<32>` is just a string, and so
can be assigned from a string.  This means you can assign a string
longer than 32 bytes, but length limits rigorously enforced during
both marshaling and unmarshaling.

## Type representations

To be consistent with go's symbol policy, all types, enum constants,
and struct/union fields defined in an XDR file are capitalized in the
corresponding go representation.  Base XDR types are mapped to their
equivalent go types:

    XDR type        Go type     notes
    --------------  ---------   ------------------------------
    bool            bool        const (TRUE=true; FALSE=false)
    int             int32
    unsigned int    uint32
    hyper           int64
    unsigned hyper  uint64
    float           float32
    double          float64
    quadruple       float128    but float128 is not defined
    string<n>       string
    opaque<n>       []byte
    opaque[n]       [n]byte
    T*              *T          for any XDR type T
    T<n>            []T         for any XDR type T
    T[n]            [n]T        for any XDR type T

Each XDR `typedef` is compiled to a go type alias.

Each XDR `enum` declaration compiles to a defined type whose
representation is an `int32`.  The constants of the enum are defined
as go constants of the new defined type.  XDR defines bools as
equivalent to an `enum` with name identifiers `TRUE` and `FALSE`.
Hence, goxdr introduces these aliases for go's `true` and `false`.  Be
sure to use the capitalized versions in case statements of XDR source
files so as to maintain compatibility with other languages and
implementations.

An XDR `struct` is compiled to a defined type represented as a go
struct containing each field of the XDR struct.

An XDR `union` is compiled to a data structure with one public field
for the discriminant and one method for each non-void "arm
declaration" (i.e., declaration in a case statement) that returns a
pointer to a value of the appropriate type.  As an example, the
following XDR:

~~~~{.c}
enum myenum {
    tag1 = 1,
    tag2 = 2,
    tag3 = 3
};

union myunion switch (myenum discriminant) {
    case tag1:
        int one;
    case tag2:
        string two<>;
    default:
        void;
};
~~~~

compiles to this go code:

~~~~{.go}
type Myenum int32
const (
    Tag1 = Myenum(1)
    Tag2 = Myenum(2)
    Tag3 = Myenum(3)
)
func XDR_Myenum(x XDR, name string, v *Myenum) {...}

type Myunion struct {
    Discriminant Myenum
    ....
}
func (u *Myunion) One() *int32 {...}
func (u *Myunion) Two() *string {...}
func XDR_Myunion(x XDR, name string, v *Myunion) {...}
~~~~

## The XDR interface

For every type `T` generated by goxdr (where `T` is the capitalized go
type), including typedefs, goxdr generates a function

~~~~{.go}
func XDR_T(x XDR, name string, v *T) {...}
~~~~
that can be used to marshal, unmarshal, or otherwise traverse the data
structure.  The `name` argument has no effect for RFC4506-compliant
binary marshaling, and can safely be supplied as the empty string
`""`.  However, when traversing an XDR type for other purposes such as
pretty-printing, `name` will be set to the nested name of the field
(with components separated by period).

The argument `x` implements the XDR interface and determines what
XDR_T actually does (i.e., marshal or unmarshal).  It has the
following interface:

~~~~{.go}
type XDR interface {
	Marshal(name string, ptr interface{})
	Sprintf(string, ...interface{}) string
}
~~~~

`Sprintf` is expected to be a copy of `fmt.Sprintf`.  However, XDR
back-ends that do not make use of the `name` argument (notably
marshaling to RFC4506 binary format) can save some overhead by
returning an empty string.  Hence, the following are the two sensible
implementations of `Sprintf`:

~~~~{.go}
func (xp *MyXDR1) Sprintf(f string, args ...interface{}) string {
	return fmt.Sprintf(f, args...)
}

func (xp *MyXDR2) Sprintf(f string, args ...interface{}) string {
	return ""
}
~~~~

`Marshal` is the method that actually does whatever work will be
applied to the data structure.  The second argument, `ptr`, will be
called with generated go value that must be traversed.  However, to
simplify traversal code, the value will be

## Generated code

# OPTIONS

goxdr supports the following options:

`-help`
:	Print a brief usage message.

`-b`
:	goxdr outputs boilerplate code to assist in marshaling and
unmarshaling values.  Only one copy of this boilerplate should be
included in a package.  If you use goxdr to compile all XDR input
files to a single go file (the recommended usage), then you will get
only one copy of the boilerplate.  However, if you compile different
XDR files into different go files, you will need to specify `-b` with
each XDR input file to avoid including the boilerplate, then run goxdr
with no input files (`goxdr -o goxdr_boilerplate.go`) to get one copy
of the boilerplate.

`-o` _output.go_
:	Write the output to file _output.go_ instead of standard output.

`-p` _package_
:	Specify the package name to use for the generated code.  The
default is for the generated code to declare `package main`.

# EXAMPLES


# ENVIRONMENT


# FILES


# SEE ALSO

rpcgen(1), xdrc(1)

<https://tools.ietf.org/html/rfc4506>

# BUGS

goxdr ignores program and version declarations, and should instead
compile them to something that can be used with to implement RFC5531
RPC interfaces.

goxdr is not hygienic.  Because it capitalizes symbols, it could
produce a name clash if two symbols differ only in the capitalization
of the first letter.  Moreover, it introduces various helper types and
functions that begin `XDR_` or `Xdr`, so could produce incorrect code
if users employ such identifiers in XDR files.  Though RFC4506
disallows identifiers that start with underscore, goxdr accepts them
and produces code with inconsistent export semantics (since underscore
cannot be capitalized).

IEEE 754 floating point allows for many different NaN (not a number)
values.  The marshaling code simply takes whatever binary value go has
sitting in memory, byteswapping on little-endian machines.  Other
languages and XDR implemenations may produce different NaN values from
the same code.  Hence, in the presence of floating point, the
marshaled output of seemingly deterministic code may vary across
implementations.