// Copyright 2017 Pulumi, Inc. All rights reserved.

package cidlc

import (
	"bufio"
	"bytes"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/pulumi/coconut/pkg/util/contract"
)

// TODO: preserve GoDocs.

type PackGenerator struct {
	Root     string
	Out      string
	Currpkg  *Package          // the package currently being visited.
	Currfile string            // the file currently being visited.
	Fhadres  bool              // true if the file had at least one resource.
	Ffimp    map[string]string // a map of foreign packages used in a file.
	Flimp    map[string]bool   // a map of imported members from modules within this package.
}

func NewPackGenerator(root string, out string) *PackGenerator {
	return &PackGenerator{
		Root: root,
		Out:  out,
	}
}

func (pg *PackGenerator) Relpath(s string) (string, error) {
	return filepath.Rel(pg.Root, s)
}

// Filename gets the target filename for any given member.
func (pg *PackGenerator) Filename(pkg *Package, m Member) (string, error) {
	prog := pkg.Program
	source := prog.Fset.Position(m.Pos()).Filename // the source filename.`
	rel, err := pg.Relpath(source)
	if err != nil {
		return "", err
	}
	return filepath.Join(pg.Out, rel), nil
}

// Generate generates a Coconut package's source code from a given compiled IDL program.
func (pg *PackGenerator) Generate(pkg *Package) error {
	// Install context about the current entity being visited.
	oldpkg, oldfile := pg.Currpkg, pg.Currfile
	pg.Currpkg = pkg
	defer (func() {
		pg.Currpkg, pg.Currfile = oldpkg, oldfile
	})()

	// Now walk through the package, file by file, and generate the contents.
	for relpath, file := range pkg.Files {
		// Make the target file by concatening the output with the relative path, and ensure the directory exists.
		path := filepath.Join(pg.Out, relpath)
		pg.EnsureDir(path)

		// Next open up a file and emit the contents according to the member type.
		var members []Member
		for _, key := range file.MemberKeys {
			members = append(members, file.Members[key])
		}
		pg.Currfile = relpath
		pg.EmitFile(path, members)
	}

	return nil
}

func (pg *PackGenerator) EnsureDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0755)
}

func (pg *PackGenerator) EmitFile(file string, members []Member) error {
	// Set up context.
	oldhadres, oldffimp, oldflimp := pg.Fhadres, pg.Ffimp, pg.Flimp
	pg.Fhadres, pg.Ffimp, pg.Flimp = false, make(map[string]string), make(map[string]bool)
	defer (func() {
		pg.Fhadres = oldhadres
		pg.Ffimp = oldffimp
		pg.Flimp = oldflimp
	})()

	// First, generate the body.  This is required first so we know which imports to emit.
	body := pg.genFileBody(members)

	// Next actually open up the file and emit the header, imports, and the body of the module.
	return pg.emitFileContents(file, body)
}

func (pg *PackGenerator) emitFileContents(file string, body string) error {
	// The output is TypeScript, so alter the extension.
	if dotindex := strings.LastIndex(file, "."); dotindex != -1 {
		file = file[:dotindex]
	}
	file += ".ts"

	// Open up a writer that overwrites whatever file contents already exist.
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)

	// Emit a header into the file.
	writefmt(w, "// *** WARNING: this file was generated by the Coconut IDL Compiler (CIDLC).  ***\n")
	writefmt(w, "// *** Do not edit by hand unless you are taking matters into your own hands! ***\n")
	writefmt(w, "\n")

	// If there are any resources, import the Coconut package.
	if pg.Fhadres {
		writefmt(w, "import * as coconut from \"@coconut/coconut\";\n")
		writefmt(w, "\n")
	}
	if len(pg.Flimp) > 0 {
		for local := range pg.Flimp {
			// For a local import, make sure to manufacture a correct relative import of the members.
			dir := filepath.Dir(file)
			module := pg.Currpkg.MemberFiles[local].Path
			relimp, err := filepath.Rel(dir, filepath.Join(pg.Out, module))
			contract.Assert(err == nil)
			var impname string
			if strings.HasPrefix(relimp, ".") {
				impname = relimp
			} else {
				impname = "./" + relimp
			}
			if filepath.Ext(impname) != "" {
				lastdot := strings.LastIndex(impname, ".")
				impname = impname[:lastdot]
			}
			writefmt(w, "import {%v} from \"%v\";\n", local, impname)
		}
		writefmt(w, "\n")
	}
	if len(pg.Ffimp) > 0 {
		for impname, pkg := range pg.Ffimp {
			contract.Failf("Foreign imports not yet supported: import=%v pkg=%v", impname, pkg)
		}
		writefmt(w, "\n")
	}

	writefmt(w, "%v\n", body)
	return w.Flush()
}

func (pg *PackGenerator) genFileBody(members []Member) string {
	// Accumulate the buffer in a string.
	var buffer bytes.Buffer
	w := bufio.NewWriter(&buffer)

	// Now go ahead and emit the code for all members of this package.
	for i, m := range members {
		if i > 0 {
			// Allow aliases and consts to pile up without line breaks.
			_, isalias := m.(*Alias)
			_, isconst := m.(*Const)
			if (!isalias && !isconst) || reflect.TypeOf(m) != reflect.TypeOf(members[i-1]) {
				writefmt(w, "\n")
			}
		}
		switch t := m.(type) {
		case *Alias:
			pg.EmitAlias(w, t)
		case *Const:
			pg.EmitConst(w, t)
		case *Enum:
			pg.EmitEnum(w, t)
		case *Resource:
			pg.EmitResource(w, t)
		case *Struct:
			pg.EmitStruct(w, t)
		default:
			contract.Failf("Unrecognized package member type: %v", reflect.TypeOf(m))
		}
	}

	writefmt(w, "\n")
	w.Flush()
	return buffer.String()
}

func (pg *PackGenerator) EmitAlias(w *bufio.Writer, alias *Alias) {
	writefmt(w, "export type %v = %v;\n", alias.Name(), pg.GenTypeName(alias.Target))
}

func (pg *PackGenerator) EmitConst(w *bufio.Writer, konst *Const) {
	writefmt(w, "export let %v: %v = %v;\n", konst.Name(), pg.GenTypeName(konst.Type), konst.Value.String())
}

func (pg *PackGenerator) EmitEnum(w *bufio.Writer, enum *Enum) {
	writefmt(w, "export type %v =\n", enum.Name())
	contract.Assert(len(enum.Values) > 0)
	for i, value := range enum.Values {
		if i > 0 {
			writefmt(w, " |\n")
		}
		writefmt(w, "    %v", value)
	}
	writefmt(w, ";\n")
}

func forEachField(t TypeMember, action func(*types.Var, PropertyOptions)) int {
	return forEachStructField(t.Struct(), t.PropertyOptions(), action)
}

func forEachStructField(s *types.Struct, opts []PropertyOptions, action func(*types.Var, PropertyOptions)) int {
	n := 0
	for i, j := 0, 0; i < s.NumFields(); i++ {
		fld := s.Field(i)
		if fld.Anonymous() {
			// For anonymous types, recurse.
			named := fld.Type().(*types.Named)
			embedded := named.Underlying().(*types.Struct)
			k := forEachStructField(embedded, opts[j:], action)
			j += k
			n += k
		} else {
			// For actual fields, invoke the action, and bump the counters.
			if action != nil {
				action(s.Field(i), opts[j])
			}
			j++
			n++
		}
	}
	return n
}

func writefmt(w *bufio.Writer, msg string, args ...interface{}) {
	w.WriteString(fmt.Sprintf(msg, args...))
}

func (pg *PackGenerator) EmitResource(w *bufio.Writer, res *Resource) {
	// Emit the full resource class definition, including constructor, etc.
	pg.emitResourceClass(w, res)
	writefmt(w, "\n")

	// Finally, emit an entire struct type for the args interface.
	pg.emitStructType(w, res, res.Name()+"Args")

	// Remember we had a resource in this file so we can import the right stuff.
	pg.Fhadres = true
}

func (pg *PackGenerator) emitResourceClass(w *bufio.Writer, res *Resource) {
	// Emit the class definition itself.
	name := res.Name()
	writefmt(w, "export class %v extends coconut.Resource implements %vArgs {\n", name, name)

	// Now all fields definitions.
	fn := forEachField(res, func(fld *types.Var, opt PropertyOptions) {
		pg.emitField(w, fld, opt, "    public ")
	})
	if fn > 0 {
		writefmt(w, "\n")
	}

	// Next, a constructor that validates arguments and self-assigns them.
	writefmt(w, "    constructor(args: %vArgs) {\n", name)
	writefmt(w, "        super();\n")
	forEachField(res, func(fld *types.Var, opt PropertyOptions) {
		// Skip output properties because they won't exist on the arguments.
		if !opt.Out {
			if !opt.Optional {
				// Validate that required parameters exist.
				writefmt(w, "        if (args.%v === undefined) {\n", opt.Name)
				writefmt(w, "            throw new Error(\"Missing required argument '%v'\");\n", opt.Name)
				writefmt(w, "        }\n")
			}
			writefmt(w, "        this.%v = args.%v;\n", opt.Name, opt.Name)
		}
	})
	writefmt(w, "    }\n")

	writefmt(w, "}\n")
}

func (pg *PackGenerator) EmitStruct(w *bufio.Writer, s *Struct) {
	pg.emitStructType(w, s, s.Name())
}

func (pg *PackGenerator) emitStructType(w *bufio.Writer, t TypeMember, name string) {
	writefmt(w, fmt.Sprintf("export interface %v {\n", name))
	forEachField(t, func(fld *types.Var, opt PropertyOptions) {
		// Skip output properties, since those exist solely on the resource class.
		if !opt.Out {
			pg.emitField(w, fld, opt, "    ")
		}
	})
	writefmt(w, "}\n")
}

func (pg *PackGenerator) emitField(w *bufio.Writer, fld *types.Var, opt PropertyOptions, prefix string) {
	var readonly string
	var optional string
	var typ string
	if opt.Replaces {
		readonly = "readonly "
	}
	if opt.Optional {
		optional = "?"
	}
	typ = pg.GenTypeName(fld.Type())
	writefmt(w, "%v%v%v%v: %v;\n", prefix, readonly, opt.Name, optional, typ)
}

// registerForeign registers that we have seen a foreign package and requests that the imports be emitted for it.
func (pg *PackGenerator) registerForeign(pkg *types.Package) string {
	path := pkg.Path()
	if impname, has := pg.Ffimp[path]; has {
		return impname
	}

	// If we haven't seen this yet, allocate an import name for it.  For now, just use the package name.
	name := pkg.Name()
	pg.Ffimp[path] = name
	return name
}

func (pg *PackGenerator) GenTypeName(t types.Type) string {
	switch u := t.(type) {
	case *types.Basic:
		switch k := u.Kind(); k {
		case types.Bool:
			return "boolean"
		case types.String:
			return "string"
		case types.Float64:
			return "number"
		default:
			contract.Failf("Unrecognized GenTypeName basic type: %v", k)
		}
	case *types.Named:
		// If this came from the same package; the imports will have arranged for it to be available by name.
		obj := u.Obj()
		pkg := obj.Pkg()
		name := obj.Name()
		if pkg == pg.Currpkg.Pkginfo.Pkg {
			// If this wasn't in the same file, we still need a relative module import to get the name in scope.
			if pg.Currpkg.MemberFiles[name].Path != pg.Currfile {
				pg.Flimp[name] = true
			}
			return name
		}

		// Otherwise, we will need to refer to a qualified import name.
		impname := pg.registerForeign(pkg)
		return fmt.Sprintf("%v.%v", impname, name)
	case *types.Map:
		return fmt.Sprintf("{[key: %v]: %v}", pg.GenTypeName(u.Key()), pg.GenTypeName(u.Elem()))
	case *types.Pointer:
		return pg.GenTypeName(u.Elem()) // no pointers in TypeScript, just emit the underlying type.
	case *types.Slice:
		return fmt.Sprintf("%v[]", pg.GenTypeName(u.Elem())) // postfix syntax for arrays.
	default:
		contract.Failf("Unrecognized GenTypeName type: %v", reflect.TypeOf(u))
	}
	return ""
}
