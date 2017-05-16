package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// defines several variables for parameterizing the protoc command. We can pull
// this out into a toml files in cases where we to vary this per package.
var (
	generationPlugin = "gogoctrd"

	preIncludePaths = []string{
		".",
	}

	// vendoredIncludes is used for packages that should be included as vendor
	// directories. We don't differentiate between packages and projects here.
	// This should just be the root of where the protos are included from.
	vendoredIncludes = []string{
		"github.com/gogo/protobuf",
	}

	// postIncludePaths defines untouched include paths to be added untouched
	// to the protoc command.
	postIncludePaths = []string{
		"/usr/local/include", // common location for protoc installation of WKTs
	}

	plugins = []string{
		"grpc",
	}

	// packageMap allows us to map protofile imports to specific Go packages. These
	// becomes the M declarations at the end of the declaration.
	packageMap = map[string]string{
		"google/protobuf/timestamp.proto":  "github.com/gogo/protobuf/types",
		"google/protobuf/any.proto":        "github.com/gogo/protobuf/types",
		"google/protobuf/field_mask.proto": "github.com/gogo/protobuf/types",
		"google/protobuf/descriptor.proto": "github.com/gogo/protobuf/protoc-gen-gogo/descriptor",
		"gogoproto/gogo.proto":             "github.com/gogo/protobuf/gogoproto",
	}

	tmpl = template.Must(template.New("protoc").Parse(`protoc -I
	{{- range $index, $include := .Includes -}}
		{{if $index}}:{{end -}}
			{{.}}
		{{- end }} --
	{{- .Name -}}_out=plugins={{- range $index, $plugin := .Plugins -}}
		{{- if $index}}+{{end}}
		{{- $plugin}}
	{{- end -}}
	,import_path={{.ImportPath}}
	{{- range $proto, $gopkg := .PackageMap -}},M
		{{- $proto}}={{$gopkg -}}
	{{- end -}}
	:{{- .OutputDir }}
	{{- range .Files}} {{.}}{{end -}}
`))
)

// Protoc defines inputs to a protoc command string.
type Protoc struct {
	Name       string // backend name
	Includes   []string
	Plugins    []string
	ImportPath string
	PackageMap map[string]string
	Files      []string
	OutputDir  string
}

func (p *Protoc) mkcmd() (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func main() {
	flag.Parse()

	pkgInfos, err := goPkgInfo(flag.Args()...)
	if err != nil {
		log.Fatalln(err)
	}

	gopath, err := gopathSrc()
	if err != nil {
		log.Fatalln(err)
	}

	gopathCurrent, err := gopathCurrent()
	if err != nil {
		log.Fatalln(err)
	}

	// For some reason, the golang protobuf generator makes the god awful
	// decision to output the files relative to the gopath root. It doesn't do
	// this only in the case where you give it ".".
	outputDir := filepath.Join(gopathCurrent, "src")

	for _, pkg := range pkgInfos {
		var includes []string
		includes = append(includes, preIncludePaths...)

		vendor, err := closestVendorDir(pkg.Dir)
		if err != nil {
			log.Fatalln(err)
		}

		// we also special case the inclusion of gogoproto in the vendor dir.
		// We could parameterize this better if we find it to be a common case.
		var vendoredIncludesResolved []string
		for _, vendoredInclude := range vendoredIncludes {
			vendoredIncludesResolved = append(vendoredIncludesResolved,
				filepath.Join(vendor, vendoredInclude))
		}

		includes = append(includes, vendoredIncludesResolved...)
		includes = append(includes, vendor, gopath)
		includes = append(includes, postIncludePaths...)

		protoc := Protoc{
			Name:       generationPlugin,
			ImportPath: pkg.GoImportPath,
			PackageMap: packageMap,
			Plugins:    plugins,
			Files:      pkg.ProtoFiles,
			OutputDir:  outputDir,
			Includes:   includes,
		}

		arg, err := protoc.mkcmd()
		if err != nil {
			log.Fatalln(err)
		}

		fmt.Println(arg)

		// pass to sh -c so we don't need to re-split here.
		args := []string{"-c", arg}
		cmd := exec.Command("sh", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Fatalf("%s %s\n", out, err)
		}
	}
}

type protoGoPkgInfo struct {
	Dir          string
	GoImportPath string
	ProtoFiles   []string
}

func goPkgInfo(golistpath ...string) ([]protoGoPkgInfo, error) {
	args := []string{
		"list", "-e", "-f", "{{.ImportPath}} {{.Dir}}"}
	args = append(args, golistpath...)
	cmd := exec.Command("go", args...)

	p, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var pkgInfos []protoGoPkgInfo
	lines := bytes.Split(p, []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		parts := bytes.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("bad output from command: %s", p)
		}

		pkgInfo := protoGoPkgInfo{
			Dir:          string(parts[1]),
			GoImportPath: string(parts[0]),
		}

		protoFiles, err := filepath.Glob(filepath.Join(pkgInfo.Dir, "*.proto"))
		if err != nil {
			return nil, err
		}
		if len(protoFiles) == 0 {
			continue // not a proto directory, skip
		}

		pkgInfo.ProtoFiles = protoFiles
		pkgInfos = append(pkgInfos, pkgInfo)
	}

	return pkgInfos, nil
}

// gopathSrc modifies GOPATH elements from env to include the src directory.
func gopathSrc() (string, error) {
	gopathAll := os.Getenv("GOPATH")

	if gopathAll == "" {
		return "", fmt.Errorf("must be run from a gopath")
	}

	var elements []string
	for _, element := range strings.Split(gopathAll, ":") { // TODO(stevvooe): Make this work on windows.
		elements = append(elements, filepath.Join(element, "src"))
	}

	return strings.Join(elements, ":"), nil
}

// gopathCurrent provides the top-level gopath for the current generation.
func gopathCurrent() (string, error) {
	gopathAll := os.Getenv("GOPATH")

	if gopathAll == "" {
		return "", fmt.Errorf("must be run from a gopath")
	}

	return strings.Split(gopathAll, ":")[0], nil
}

// closestVendorDir walks up from dir until it finds the vendor directory.
func closestVendorDir(dir string) (string, error) {
	dir = filepath.Clean(dir)
	for dir != "" && dir != string(filepath.Separator) { // TODO(stevvooe): May not work on windows
		vendor := filepath.Join(dir, "vendor")
		fi, err := os.Stat(vendor)
		if err != nil {
			if os.IsNotExist(err) {
				// up we go!
				dir = filepath.Dir(dir)
				continue
			}
			return "", err
		}

		if !fi.IsDir() {
			// up we go!
			dir = filepath.Dir(dir)
			continue
		}

		return vendor, nil
	}

	return "", fmt.Errorf("no vendor dir found")
}
