package translationcheck

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template/parse"
)

// SourceUsage finds references in executable Go and template syntax. Dynamic
// consumers have finite, reviewed contracts below; their IDs are derived from
// the actual producer syntax, never from a second list of translation keys.
func SourceUsage(root string) (map[string]string, []Issue) {
	s := &usageScanner{root: root, fset: token.NewFileSet(), functions: map[string]*ast.FuncDecl{}, types: map[string]ast.Expr{}, used: map[string]string{}, seen: map[string]bool{}, receivers: map[string]bool{"l": true, "translations": true}}
	for _, dir := range []string{"internal/web", "internal/languages"} {
		files, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
		if err != nil {
			s.problem(dir, 0, err.Error())
			continue
		}
		for _, filename := range files {
			if strings.HasSuffix(filename, "_test.go") {
				continue
			}
			path, _ := filepath.Rel(root, filename)
			contents, err := os.ReadFile(filename)
			if err != nil {
				s.problem(path, 0, "read source: "+err.Error())
				continue
			}
			file, err := parser.ParseFile(s.fset, path, contents, 0)
			if err != nil {
				s.problem(path, 0, err.Error())
				continue
			}
			s.files = append(s.files, file)
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					s.functions[path+":"+fn.Name.Name] = fn
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if spec, ok := n.(*ast.TypeSpec); ok {
					s.types[spec.Name.Name] = spec.Type
				}
				return true
			})
		}
	}
	s.findReceivers()
	for id, fn := range s.functions {
		path := strings.Split(id, ":")[0]
		if !strings.HasPrefix(path, "internal/web/") {
			continue
		}
		s.scanGoCalls(id, fn, fn.Body)
	}
	for _, file := range s.files {
		path := s.fset.Position(file.Pos()).Filename
		if strings.HasPrefix(path, "internal/web/") {
			s.scanGoCalls(path+":<package>", nil, file)
		}
	}
	s.scanTemplates()
	for contract := range dynamicConsumers {
		parts := strings.Split(contract, ":")
		if _, err := os.Stat(filepath.Join(root, parts[0])); err == nil && !s.seen[contract] {
			s.problem(parts[0], 0, "stale dynamic translation contract: "+contract)
		}
	}
	for contract := range dynamicTemplates {
		path := strings.Split(contract, ":")[0]
		if _, err := os.Stat(filepath.Join(root, path)); err == nil && !s.seen[contract] {
			s.problem(path, 0, "stale dynamic template translation contract: "+contract)
		}
	}
	sort.Slice(s.issues, func(i, j int) bool {
		a, b := s.issues[i], s.issues[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Message < b.Message
	})
	return s.used, s.issues
}

func (s *usageScanner) scanGoCalls(id string, fn *ast.FuncDecl, node ast.Node) {
	ast.Inspect(node, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncDecl); ok && fn == nil {
			return false
		}
		if literal, ok := n.(*ast.CompositeLit); ok && strings.HasSuffix(expression(literal.Type), "LocalizeConfig") {
			for _, element := range literal.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok || expression(field.Key) != "MessageID" {
					continue
				}
				if value, ok := stringLiteral(field.Value); ok {
					if strings.TrimSpace(value) == "" {
						s.at(field.Value, "translation message ID is empty")
					} else {
						s.add(value, field.Value.Pos())
					}
					continue
				}
				forwarder := id + ":" + expression(field.Value)
				if forwarder != "internal/web/localization.go:Text:messageID" && forwarder != "internal/web/localization.go:Count:messageID" && forwarder != "internal/web/localization.go:Format:id" {
					s.at(field.Value, "unrecognized dynamic LocalizeConfig message ID: "+forwarder)
				}
			}
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (method.Sel.Name != "Text" && method.Sel.Name != "Count" && method.Sel.Name != "Format") {
			return true
		}
		if !s.isReceiver(method.X) {
			return true
		}
		if len(call.Args) < 2 {
			s.at(call, "localization call has no message ID")
			return true
		}
		if literal, ok := stringLiteral(call.Args[1]); ok {
			if strings.TrimSpace(literal) == "" {
				s.at(call.Args[1], "translation message ID is empty")
			} else {
				s.add(literal, call.Args[1].Pos())
			}
			return true
		}
		contract := id + ":" + expression(call.Args[1])
		if !dynamicConsumers[contract] {
			s.at(call.Args[1], "unrecognized dynamic translation ID: "+contract)
			return true
		}
		s.seen[contract] = true
		values, err := s.dynamic(id, fn, call.Args[1])
		if err == nil && len(values) == 0 {
			err = fmt.Errorf("producer has no literal message IDs")
		}
		if err != nil {
			s.at(call.Args[1], "cannot resolve dynamic translation ID: "+err.Error())
		} else {
			s.addValues(values)
		}
		return true
	})
}

type usageScanner struct {
	root      string
	fset      *token.FileSet
	files     []*ast.File
	functions map[string]*ast.FuncDecl
	types     map[string]ast.Expr
	used      map[string]string
	issues    []Issue
	seen      map[string]bool
	receivers map[string]bool
}

func (s *usageScanner) isReceiver(expr ast.Expr) bool {
	if selector, ok := expr.(*ast.SelectorExpr); ok && selector.Sel.Name == "localization" {
		return true
	}
	if call, ok := expr.(*ast.CallExpr); ok && (expression(call.Fun) == "newLocalization" || expression(call.Fun) == "buildLocalization") {
		return true
	}
	if pointer, ok := expr.(*ast.UnaryExpr); ok && pointer.Op == token.AND {
		if literal, ok := pointer.X.(*ast.CompositeLit); ok && expression(literal.Type) == "localization" {
			return true
		}
	}
	return s.receivers[expression(expr)]
}

// Follow finite receiver aliases so renaming a localizer variable does not
// silently make its translation references disappear.
func (s *usageScanner) findReceivers() {
	for changed := true; changed; {
		changed = false
		add := func(name string) {
			if !s.receivers[name] {
				s.receivers[name] = true
				changed = true
			}
		}
		for _, file := range s.files {
			ast.Inspect(file, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.Field:
					if expression(v.Type) == "*localization" {
						for _, name := range v.Names {
							add(name.Name)
						}
					}
				case *ast.AssignStmt:
					for i, rhs := range v.Rhs {
						if i < len(v.Lhs) && s.isReceiver(rhs) {
							add(expression(v.Lhs[i]))
						}
					}
				case *ast.ValueSpec:
					for i, name := range v.Names {
						if (v.Type != nil && expression(v.Type) == "*localization") || (i < len(v.Values) && s.isReceiver(v.Values[i])) {
							add(name.Name)
						}
					}
				}
				return true
			})
		}
	}
}
func (s *usageScanner) problem(path string, line int, message string) {
	s.issues = append(s.issues, Issue{Path: path, Line: line, Message: message})
}
func (s *usageScanner) at(n ast.Node, message string) {
	p := s.fset.Position(n.Pos())
	s.problem(p.Filename, p.Line, message)
}
func (s *usageScanner) add(value string, pos token.Pos) {
	if value == "" {
		return
	}
	p := s.fset.Position(pos)
	loc := fmt.Sprintf("%s:%d", p.Filename, p.Line)
	if old, ok := s.used[value]; !ok || loc < old {
		s.used[value] = loc
	}
}
func (s *usageScanner) addValues(values keyValues) {
	for value, pos := range values {
		s.add(value, pos)
	}
}
func expression(n ast.Node) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, token.NewFileSet(), n)
	return b.String()
}
func stringLiteral(e ast.Expr) (string, bool) {
	l, ok := e.(*ast.BasicLit)
	if !ok || l.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(l.Value)
	return v, err == nil
}

type keyValues map[string]token.Pos

func mergeValues(dst, src keyValues) {
	for key, pos := range src {
		dst[key] = pos
	}
}

// Only these dynamic call shapes are supported. An added dynamic consumer must
// be reviewed and given a finite source producer; literal calls need no setup.
var dynamicConsumers = map[string]bool{
	"internal/web/public.go:base:definition.SwitchMessageID":                      true,
	"internal/web/public.go:incidentForLanguage:languageDefinition.StepMessageID": true,
	"internal/web/public.go:metadataCodeLabel:key":                                true,
	"internal/web/legal.go:legal:title":                                           true,
	"internal/web/legal.go:legal:title + \"Description\"":                         true,
	"internal/web/legal.go:writeMarkdownFrontMatterBuilder:data.Title":            true,
	"internal/web/legal.go:writeMarkdownFrontMatterBuilder:section.Title":         true,
	"internal/web/legal.go:writeMarkdownFrontMatterBuilder:section.Copy":          true,
	"internal/web/licenses.go:licenses:g.Heading":                                 true,
	"internal/web/reader.go:groupByIncident:kind":                                 true,
	"internal/web/reader.go:decorateReader:label":                                 true,
	"internal/web/discovery.go:renderTimelineMarkdown:copyKey":                    true,
	"internal/web/discovery.go:renderDetailMarkdown:sourceCopy":                   true,
	"internal/web/discovery.go:renderAboutMarkdown:step.title":                    true,
	"internal/web/discovery.go:renderAboutMarkdown:step.copy":                     true,
	"internal/web/discovery.go:renderAboutMarkdown:section.title":                 true,
	"internal/web/discovery.go:renderAboutMarkdown:key":                           true,
	"internal/web/contact.go:renderContact:key":                                   true,
	"internal/web/contact.go:renderContact:data.Error":                            true,
	"internal/web/map.go:mapPage:p.key":                                           true,
	"internal/web/map.go:mapPage:bound.label":                                     true,
	"internal/web/map.go:mapPage:v.key":                                           true,
	"internal/web/map.go:renderMapMarkdown:key":                                   true,
}

var dynamicTemplates = map[string]bool{
	"internal/web/templates/legal.html:.Title":      true,
	"internal/web/templates/legal.html:.Copy":       true,
	"internal/web/templates/licenses.html:.Heading": true,
	"internal/web/templates/contact.html:.Error":    true,
	"internal/web/templates/contact.html:$key":      true,
	"internal/web/templates/contact.html:.":         true,
}

func (s *usageScanner) dynamic(id string, fn *ast.FuncDecl, e ast.Expr) (keyValues, error) {
	name := expression(e)
	switch {
	case name == "definition.SwitchMessageID":
		return s.registryKeys("SwitchMessageID")
	case name == "languageDefinition.StepMessageID":
		return s.registryKeys("StepMessageID")
	case strings.HasSuffix(id, ":writeMarkdownFrontMatterBuilder"):
		if name == "data.Title" {
			return s.producer("internal/web/legal.go:legal", "title")
		}
		return s.producer("internal/web/legal.go:legal", "data.Sections."+strings.TrimPrefix(name, "section."))
	case id == "internal/web/licenses.go:licenses" && name == "g.Heading":
		return s.resolve(fn, parseExpr("group.Heading"), nil, nil, map[string]bool{})
	case id == "internal/web/contact.go:renderContact" && name == "data.Error":
		return s.contactErrors()
	case id == "internal/web/reader.go:decorateReader" && name == "label":
		values, err := s.resolve(fn, e, nil, nil, map[string]bool{})
		if err != nil {
			return nil, err
		}
		args, err := s.callArguments(fn, "add", 1)
		mergeValues(values, args)
		return values, err
	default:
		return s.resolve(fn, e, nil, nil, map[string]bool{})
	}
}

func parseExpr(value string) ast.Expr { e, _ := parser.ParseExpr(value); return e }
func (s *usageScanner) producer(id, expr string) (keyValues, error) {
	fn := s.functions[id]
	if fn == nil {
		return nil, fmt.Errorf("missing producer %s", id)
	}
	return s.resolve(fn, parseExpr(expr), nil, nil, map[string]bool{})
}
func (s *usageScanner) registryKeys(field string) (keyValues, error) {
	values := keyValues{}
	for _, file := range s.files {
		if s.fset.Position(file.Pos()).Filename != "internal/languages/languages.go" {
			continue
		}
		var invalid bool
		ast.Inspect(file, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if ok && expression(kv.Key) == field {
				if value, ok := stringLiteral(kv.Value); ok {
					values[value] = kv.Value.Pos()
				} else {
					invalid = true
				}
			}
			return true
		})
		if invalid {
			return nil, fmt.Errorf("registered %s must use literal message IDs", field)
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("no literal registered %s values", field)
	}
	return values, nil
}

// resolve evaluates only finite key-producing syntax. It does not execute Go,
// inspect data files, or infer keys from arbitrary text or prefixes.
func (s *usageScanner) resolve(fn *ast.FuncDecl, e ast.Expr, path []string, typ ast.Expr, stack map[string]bool) (keyValues, error) {
	if e == nil {
		return nil, fmt.Errorf("missing expression")
	}
	if literal, ok := stringLiteral(e); ok && len(path) == 0 {
		return keyValues{literal: e.Pos()}, nil
	}
	if named, ok := typ.(*ast.Ident); ok {
		typ = s.types[named.Name]
	}
	switch v := e.(type) {
	case *ast.ParenExpr:
		return s.resolve(fn, v.X, path, typ, stack)
	case *ast.SelectorExpr:
		return s.resolve(fn, v.X, append([]string{v.Sel.Name}, path...), typ, stack)
	case *ast.IndexExpr:
		return s.resolve(fn, v.X, path, typ, stack)
	case *ast.BinaryExpr:
		if v.Op == token.ADD && len(path) == 0 {
			a, err := s.resolve(fn, v.X, nil, nil, stack)
			if err != nil {
				return nil, err
			}
			b, err := s.resolve(fn, v.Y, nil, nil, stack)
			if err != nil {
				return nil, err
			}
			out := keyValues{}
			for x, pos := range a {
				for y := range b {
					out[x+y] = pos
				}
			}
			return out, nil
		}
	case *ast.CallExpr:
		if expression(v.Fun) == "append" {
			out := keyValues{}
			for _, arg := range v.Args[1:] {
				values, err := s.resolve(fn, arg, path, typ, stack)
				if err != nil {
					return nil, err
				}
				mergeValues(out, values)
			}
			return out, nil
		}
	case *ast.CompositeLit:
		if v.Type != nil {
			typ = v.Type
			if named, ok := typ.(*ast.Ident); ok {
				typ = s.types[named.Name]
			}
		}
		out := keyValues{}
		var fields []string
		if structure, ok := typ.(*ast.StructType); ok {
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					fields = append(fields, name.Name)
				}
			}
		}
		for index, item := range v.Elts {
			nextPath := path
			value := item
			var childType ast.Expr
			if array, ok := typ.(*ast.ArrayType); ok {
				childType = array.Elt
			}
			if kv, ok := item.(*ast.KeyValueExpr); ok {
				value = kv.Value
				if _, isMap := typ.(*ast.MapType); !isMap && len(path) > 0 {
					if expression(kv.Key) != path[0] {
						continue
					}
					nextPath = path[1:]
				}
			} else if len(fields) > 0 && len(path) > 0 {
				if index >= len(fields) || fields[index] != path[0] {
					continue
				}
				nextPath = path[1:]
			}
			values, err := s.resolve(fn, value, nextPath, childType, stack)
			if err != nil {
				return nil, err
			}
			mergeValues(out, values)
		}
		return out, nil
	case *ast.Ident:
		identity := v.Name + "." + strings.Join(path, ".")
		if stack[identity] {
			return nil, fmt.Errorf("recursive producer %s", identity)
		}
		stack[identity] = true
		defer delete(stack, identity)
		out := keyValues{}
		var found []struct {
			expr ast.Expr
			path []string
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range node.Lhs {
					if i >= len(node.Rhs) {
						continue
					}
					left := expression(lhs)
					target := v.Name
					if len(path) > 0 {
						target += "." + strings.Join(path, ".")
					}
					if left == target {
						found = append(found, struct {
							expr ast.Expr
							path []string
						}{node.Rhs[i], nil})
					} else if strings.HasPrefix(target, left+".") {
						found = append(found, struct {
							expr ast.Expr
							path []string
						}{node.Rhs[i], strings.Split(strings.TrimPrefix(target, left+"."), ".")})
					}
				}
			case *ast.ValueSpec:
				for i, name := range node.Names {
					if name.Name == v.Name && i < len(node.Values) {
						found = append(found, struct {
							expr ast.Expr
							path []string
						}{node.Values[i], path})
					}
				}
			case *ast.RangeStmt:
				if node.Value != nil && expression(node.Value) == v.Name && (v.Pos() < fn.Pos() || (node.Body.Pos() <= v.Pos() && v.Pos() <= node.Body.End())) {
					found = append(found, struct {
						expr ast.Expr
						path []string
					}{node.X, path})
				}
			}
			return true
		})
		for _, candidate := range found {
			values, err := s.resolve(fn, candidate.expr, candidate.path, nil, stack)
			if err != nil {
				return nil, err
			}
			mergeValues(out, values)
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("non-finite producer %s%s", expression(e), strings.Join(path, "."))
}

func (s *usageScanner) callArguments(fn *ast.FuncDecl, name string, index int) (keyValues, error) {
	out := keyValues{}
	var failure error
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || expression(call.Fun) != name || len(call.Args) <= index {
			return true
		}
		values, err := s.resolve(fn, call.Args[index], nil, nil, map[string]bool{})
		if err != nil {
			failure = err
		} else {
			mergeValues(out, values)
		}
		return true
	})
	if failure != nil {
		return nil, failure
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("missing %s call arguments", name)
	}
	return out, nil
}
func (s *usageScanner) contactErrors() (keyValues, error) {
	fn := s.functions["internal/web/contact.go:submitContact"]
	if fn == nil {
		return nil, fmt.Errorf("missing contact error producer")
	}
	out, err := s.callArguments(fn, "invalid", 1)
	if err != nil {
		return nil, err
	}
	for _, producer := range []struct{ id, expr string }{{"internal/web/contact.go:submitContact", "key"}, {"internal/web/contact.go:renderContact", "data.Error"}} {
		values, e := s.producer(producer.id, producer.expr)
		if e != nil {
			return nil, e
		}
		mergeValues(out, values)
	}
	return out, nil
}

func (s *usageScanner) scanTemplates() {
	files, err := filepath.Glob(filepath.Join(s.root, "internal/web/templates/*.html"))
	if err != nil {
		s.problem("internal/web/templates", 0, err.Error())
		return
	}
	for _, filename := range files {
		path, _ := filepath.Rel(s.root, filename)
		source, err := os.ReadFile(filename)
		if err != nil {
			s.problem(path, 0, "read template: "+err.Error())
			continue
		}
		// SkipFuncCheck permits unrelated web helpers; translation helper argument
		// shapes are checked below from the parsed template command nodes.
		tree := parse.New(path)
		tree.Mode = parse.SkipFuncCheck
		trees := map[string]*parse.Tree{}
		_, err = tree.Parse(string(source), "", "", trees)
		if err != nil {
			s.problem(path, 0, "parse template: "+err.Error())
			continue
		}
		for _, item := range trees {
			s.walkTemplate(item.Root, func(command *parse.CommandNode) {
				if len(command.Args) == 0 {
					return
				}
				helper, ok := command.Args[0].(*parse.IdentifierNode)
				if !ok || (helper.Ident != "t" && helper.Ident != "tc" && helper.Ident != "message") {
					return
				}
				line := 1 + bytes.Count(source[:min(int(command.Pos), len(source))], []byte("\n"))
				if len(command.Args) < 3 {
					s.problem(path, line, "translation helper has no message ID")
					return
				}
				if literal, ok := command.Args[2].(*parse.StringNode); ok {
					if strings.TrimSpace(literal.Text) == "" {
						s.problem(path, line, "translation message ID is empty")
						return
					}
					location := fmt.Sprintf("%s:%d", path, line)
					if old, ok := s.used[literal.Text]; !ok || location < old {
						s.used[literal.Text] = location
					}
					return
				}
				key := command.Args[2].String()
				values, e := s.templateDynamic(path, key)
				if e != nil {
					s.problem(path, line, e.Error())
				} else {
					s.seen[path+":"+key] = true
					s.addValues(values)
				}
			})
		}
	}
}

func (s *usageScanner) templateDynamic(path, key string) (keyValues, error) {
	switch path + ":" + key {
	case "internal/web/templates/legal.html:.Title":
		titles, err := s.producer("internal/web/legal.go:legal", "title")
		if err != nil {
			return nil, err
		}
		sections, err := s.producer("internal/web/legal.go:legal", "data.Sections.Title")
		mergeValues(titles, sections)
		return titles, err
	case "internal/web/templates/legal.html:.Copy":
		return s.producer("internal/web/legal.go:legal", "data.Sections.Copy")
	case "internal/web/templates/licenses.html:.Heading":
		return s.producer("internal/web/licenses.go:licenses", "group.Heading")
	case "internal/web/templates/contact.html:.Error", "internal/web/templates/contact.html:$key", "internal/web/templates/contact.html:.":
		return s.contactErrors()
	default:
		return nil, fmt.Errorf("unrecognized dynamic template translation ID: %s:%s", path, key)
	}
}

func (s *usageScanner) walkTemplate(node parse.Node, visit func(*parse.CommandNode)) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *parse.ListNode:
		if n != nil {
			for _, child := range n.Nodes {
				s.walkTemplate(child, visit)
			}
		}
	case *parse.ActionNode:
		s.walkTemplate(n.Pipe, visit)
	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, command := range n.Cmds {
			s.walkTemplate(command, visit)
		}
	case *parse.CommandNode:
		visit(n)
		for _, arg := range n.Args {
			s.walkTemplate(arg, visit)
		}
	case *parse.IfNode:
		s.walkTemplate(n.Pipe, visit)
		s.walkTemplate(n.List, visit)
		s.walkTemplate(n.ElseList, visit)
	case *parse.RangeNode:
		s.walkTemplate(n.Pipe, visit)
		s.walkTemplate(n.List, visit)
		s.walkTemplate(n.ElseList, visit)
	case *parse.WithNode:
		s.walkTemplate(n.Pipe, visit)
		s.walkTemplate(n.List, visit)
		s.walkTemplate(n.ElseList, visit)
	case *parse.TemplateNode:
		s.walkTemplate(n.Pipe, visit)
	}
}
