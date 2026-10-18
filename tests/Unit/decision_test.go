package unit_test

import (
	"go/ast"
	"reflect"
	"regexp"
	"testing"

	faiss "github.com/hyz-is/arandu-faiss"
)

// TestNothingHereDecides holds the rule this package exists under: it answers
// candidates, and whether any of them is the same person is the application's
// decision, taken with its own thresholds.
//
// Two shapes would break it, and both are visible in syntax. A type or a field
// carrying a verdict -- IsMatch, Duplicate, SamePerson, Approved -- is a
// decision with an engine's name on it. A function that searches and applies a
// threshold behind one name -- FindDuplicates, MatchFace -- hides where the
// line was drawn. The names are what an assistant reaches for first, which is
// why they are refused by name.
func TestNothingHereDecides(t *testing.T) {
	t.Parallel()

	verdict := regexp.MustCompile(`(?i)(match|duplicat|same|identical|verif|approv|reject|decision|decide|verdict|accept|threshold|passed|fraud|genuine|suspect)`)
	for _, source := range productionGoFiles(t, packageRoot(t)) {
		ast.Inspect(source.file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.FuncDecl:
				if node.Name.IsExported() && verdict.MatchString(node.Name.Name) {
					t.Errorf("%s declares %s, a name that decides", source.path, node.Name.Name)
				}
			case *ast.TypeSpec:
				if node.Name.IsExported() && verdict.MatchString(node.Name.Name) {
					t.Errorf("%s declares the type %s, a name that decides", source.path, node.Name.Name)
				}
				if st, ok := node.Type.(*ast.StructType); ok {
					for _, field := range st.Fields.List {
						for _, name := range field.Names {
							if name.IsExported() && verdict.MatchString(name.Name) {
								t.Errorf("%s: %s.%s is a verdict field", source.path, node.Name.Name, name.Name)
							}
						}
					}
				}
			}
			return true
		})
	}
}

// TestTheResultsHoldNoBoolean holds the stricter rule for what a search
// answers: ids, scores and provenance, and no boolean anywhere in them,
// because a boolean on a search result is a decision whatever it is called.
func TestTheResultsHoldNoBoolean(t *testing.T) {
	t.Parallel()

	for name, typ := range map[string]reflect.Type{
		"Result":     reflect.TypeOf(faiss.Result{}),
		"Candidate":  reflect.TypeOf(faiss.Candidate{}),
		"Provenance": reflect.TypeOf(faiss.Provenance{}),
	} {
		noBoolean(t, name, typ)
	}
}

// noBoolean fails for every boolean reachable from typ, through fields,
// pointers, slices, arrays and maps.
func noBoolean(t *testing.T, root string, typ reflect.Type) {
	t.Helper()

	var walk func(path string, typ reflect.Type, seen map[reflect.Type]bool)
	walk = func(path string, typ reflect.Type, seen map[reflect.Type]bool) {
		switch typ.Kind() {
		case reflect.Bool:
			t.Errorf("%s is a boolean", path)
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(path+"[]", typ.Elem(), seen)
		case reflect.Map:
			walk(path+"[key]", typ.Key(), seen)
			walk(path+"[]", typ.Elem(), seen)
		case reflect.Struct:
			if seen[typ] {
				return
			}
			seen[typ] = true
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				walk(path+"."+f.Name, f.Type, seen)
			}
		}
	}
	walk(root, typ, map[reflect.Type]bool{})
}
