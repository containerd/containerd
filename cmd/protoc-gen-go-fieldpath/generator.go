/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package main

import (
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type generator struct {
	out *protogen.GeneratedFile
}

func newGenerator(out *protogen.GeneratedFile) *generator {
	gen := generator{out: out}
	return &gen
}

func (gen *generator) genFieldMethod(m *protogen.Message) {
	p := gen.out

	p.P("// Field returns the value for the given fieldpath as a string, if defined.")
	p.P("// If the value is not defined, the second value will be false.")
	p.P("func (m *", m.GoIdent, ") Field(fieldpath []string) (string, bool) {")

	var (
		fields    []*protogen.Field
		unhandled []*protogen.Field
	)

	for _, f := range m.Fields {
		if f.Desc.Kind() == protoreflect.BoolKind ||
			f.Desc.Kind() == protoreflect.StringKind ||
			isLabelsField(f) || isAnyField(f) || isMessageField(f) {
			fields = append(fields, f)
		} else {
			unhandled = append(unhandled, f)
		}

	}

	if len(fields) > 0 {
		p.P("if len(fieldpath) == 0 {")
		p.P(`return "", false`)
		p.P("}")

		p.P("switch fieldpath[0] {")

		for _, f := range unhandled {
			p.P("// unhandled: ", f.Desc.Name())
		}

		for _, f := range fields {
			p.P(`case "`, f.Desc.Name(), `":`)
			switch {
			case isLabelsField(f):
				stringsJoin := gen.out.QualifiedGoIdent(protogen.GoIdent{
					GoImportPath: "strings",
					GoName:       "Join",
				})

				p.P(`// Labels fields have been special-cased by name. If this breaks,`)
				p.P(`// add better special casing to fieldpath plugin.`)
				p.P("if len(m.", f.GoName, ") == 0 {")
				p.P(`return "", false`)
				p.P("}")
				p.P("value, ok := m.", f.GoName, "[", stringsJoin, `(fieldpath[1:], ".")]`)
				p.P("return value, ok")
			case isAnyField(f):
				typeurlUnmarshalAny := gen.out.QualifiedGoIdent(protogen.GoIdent{
					GoImportPath: "github.com/containerd/typeurl/v2",
					GoName:       "UnmarshalAny",
				})

				p.P("decoded, err := ", typeurlUnmarshalAny, "(m.", f.GoName, ")")
				p.P("if err != nil {")
				p.P(`return "", false`)
				p.P("}")
				p.P("adaptor, ok := decoded.(interface{ Field([]string) (string, bool) })")
				p.P("if !ok {")
				p.P(`return "", false`)
				p.P("}")
				p.P("return adaptor.Field(fieldpath[1:])")
			case isMessageField(f):
				p.P(`// NOTE(stevvooe): This is probably not correct in many cases.`)
				p.P(`// We assume that the target message also implements the Field`)
				p.P(`// method, which isn't likely true in a lot of cases.`)
				p.P(`//`)
				p.P(`// If you have a broken build and have found this comment,`)
				p.P(`// you may be closer to a solution.`)
				p.P("if m.", f.GoName, " == nil {")
				p.P(`return "", false`)
				p.P("}")
				p.P("return m.", f.GoName, ".Field(fieldpath[1:])")
			case f.Desc.Kind() == protoreflect.StringKind:
				p.P("return string(m.", f.GoName, "), len(m.", f.GoName, ") > 0")
			case f.Desc.Kind() == protoreflect.BoolKind:
				fmtSprint := gen.out.QualifiedGoIdent(protogen.GoIdent{
					GoImportPath: "fmt",
					GoName:       "Sprint",
				})

				p.P("return ", fmtSprint, "(m.", f.GoName, "), true")
			}
		}

		p.P("}")
	} else {
		for _, f := range unhandled {
			p.P("// unhandled: ", f.Desc.Name())
		}
	}

	p.P(`return "", false`)
	p.P("}")
}

func isMessageField(f *protogen.Field) bool {
	return f.Desc.Kind() == protoreflect.MessageKind && f.Desc.Cardinality() != protoreflect.Repeated && f.Message.GoIdent.GoName != "Timestamp"
}

func isLabelsField(f *protogen.Field) bool {
	return f.Desc.Kind() == protoreflect.MessageKind && f.Desc.Name() == "labels"
}

func isAnyField(f *protogen.Field) bool {
	return f.Desc.Kind() == protoreflect.MessageKind && f.Message.GoIdent.GoName == "Any"
}

func collectChildlen(parent *protogen.Message) ([]*protogen.Message, error) {
	var children []*protogen.Message
	for _, child := range parent.Messages {
		if child.Desc.IsMapEntry() {
			continue
		}
		children = append(children, child)

		xs, err := collectChildlen(child)
		if err != nil {
			return nil, err
		}
		children = append(children, xs...)
	}
	return children, nil
}

func generate(plugin *protogen.Plugin, input *protogen.File) error {
	var messages []*protogen.Message
	for _, m := range input.Messages {
		messages = append(messages, m)
		children, err := collectChildlen(m)
		if err != nil {
			return err
		}
		messages = append(messages, children...)
	}

	if len(messages) == 0 {
		// Don't generate a Go file, if that would be empty.
		return nil
	}

	file := plugin.NewGeneratedFile(input.GeneratedFilenamePrefix+"_fieldpath.pb.go", input.GoImportPath)
	file.P("// Code generated by protoc-gen-go-fieldpath. DO NOT EDIT.")
	file.P("// source: ", input.Desc.Path())
	file.P("package ", input.GoPackageName)

	gen := newGenerator(file)

	for _, m := range messages {
		gen.genFieldMethod(m)
	}
	return nil
}
