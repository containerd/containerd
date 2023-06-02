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
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
)

// defines several variables for parameterizing the protoc command. We can pull
// this out into a toml files in cases where we to vary this per package.
var (
	configPath string
	dryRun     bool
	quiet      bool
)

func init() {
	flag.StringVar(&configPath, "f", "Protobuild.toml", "override default config location")
	flag.BoolVar(&dryRun, "dryrun", false, "prints commands without running")
	flag.BoolVar(&quiet, "quiet", false, "suppress verbose output")
}

func parseVersion(s string) (int, error) {
	if s == "unstable" {
		return 0, nil
	}

	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("unknown file version %q: %w", s, err)
	}

	if v < 1 || v > 2 {
		return 0, fmt.Errorf(`unknown file version %q; valid versions are "unstable", "1" and "2"`, s)
	}
	return v, nil
}

func importPath(base, target string) (string, error) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func main() {
	flag.Parse()

	c, err := readConfig(configPath)
	if err != nil {
		log.Fatalln(err)
	}

	version, err := parseVersion(c.Version)
	if err != nil {
		log.Fatalln(err)
	}

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

	// Index overrides by target import path
	overrides := map[string]struct {
		Prefixes   []string
		Generator  string
		Generators []string
		Parameters map[string]map[string]string
		Plugins    *[]string
	}{}
	for _, override := range c.Overrides {
		for _, prefix := range override.Prefixes {
			overrides[prefix] = override
		}
	}

	// Create include paths used to find the descriptor proto. As a special case
	// we search the vendor directory relative to the current working directory.
	// This handles the case where a user has vendored their descriptor proto.
	var descriptorIncludes []string
	descriptorIncludes = append(descriptorIncludes, c.Includes.Before...)
	for _, vendorPath := range c.Includes.Vendored {
		descriptorIncludes = append(descriptorIncludes,
			filepath.Join("vendor", vendorPath))
	}
	descriptorIncludes = append(descriptorIncludes, c.Includes.Packages...)
	descriptorIncludes = append(descriptorIncludes, c.Includes.After...)
	descProto, includeDir, err := descriptorProto(descriptorIncludes)
	if err != nil {
		log.Fatalln(err)
	}

	// Aggregate descriptors for each descriptor prefix.
	descriptorSets := map[string]*descriptorSet{}
	for _, stable := range c.Descriptors {
		descriptorSets[stable.Prefix] = newDescriptorSet(stable.IgnoreFiles, descProto, includeDir)
	}

	shouldGenerateDescriptors := func(p string) bool {
		for prefix := range descriptorSets {
			if strings.HasPrefix(p, prefix) {
				return true
			}
		}

		return false
	}

	var descriptors []*descriptor.FileDescriptorSet
	for _, pkg := range pkgInfos {
		var includes []string
		includes = append(includes, c.Includes.Before...)

		vendor, err := closestVendorDir(pkg.Dir)
		if err != nil {
			if err != errVendorNotFound {
				log.Fatalln(err)
			}
		}

		if vendor != "" {
			// TODO(stevvooe): The use of the closest vendor directory is a
			// little naive. We should probably resolve all possible vendor
			// directories or at least match Go's behavior.

			// we also special case the inclusion of gogoproto in the vendor dir.
			// We could parameterize this better if we find it to be a common case.
			var vendoredIncludesResolved []string
			for _, vendoredInclude := range c.Includes.Vendored {
				vendoredIncludesResolved = append(vendoredIncludesResolved,
					filepath.Join(vendor, vendoredInclude))
			}

			// Also do this for pkg includes.
			for _, pkgInclude := range c.Includes.Packages {
				vendoredIncludesResolved = append(vendoredIncludesResolved,
					filepath.Join(vendor, pkgInclude))
			}

			includes = append(includes, vendoredIncludesResolved...)
			includes = append(includes, vendor)
		} else if len(c.Includes.Vendored) > 0 {
			log.Println("ignoring vendored includes: vendor directory not found")
		}

		// handle packages that we want to have as an include root from any of
		// the gopaths.
		for _, pkg := range c.Includes.Packages {
			includes = append(includes, gopathJoin(gopath, pkg))
		}

		includes = append(includes, gopath)
		includes = append(includes, c.Includes.After...)

		protoc := protocCmd{
			Generators: generators(c.Generators, outputDir),
			Files:      pkg.ProtoFiles,
			OutputDir:  outputDir,
			Includes:   includes,
			Version:    version,
			Names:      c.Generators,
			Plugins:    c.Plugins,
			ImportPath: pkg.GoImportPath,
			PackageMap: c.Packages,
		}

		importDirPath, err := importPath(outputDir, pkg.Dir)
		if err != nil {
			log.Fatalln(err)
		}

		parameters := map[string]map[string]string{}
		for proto, pkg := range c.Packages {
			parameters["go"] = mergeMap(parameters["go"], map[string]string{
				fmt.Sprintf("M%s", proto): pkg,
			})
		}

		for k, v := range c.Parameters {
			parameters[k] = mergeMap(parameters[k], v)
		}
		if override, ok := overrides[importDirPath]; ok {
			// selectively apply the overrides to the protoc structure.
			if len(override.Generators) > 0 {
				protoc.Names = override.Generators
				protoc.Generators = generators(override.Generators, outputDir)
			}

			if override.Plugins != nil {
				protoc.Plugins = *override.Plugins
			}

			for k, v := range override.Parameters {
				parameters[k] = mergeMap(parameters[k], v)
			}
		}

		// Set parameters per generator
		for i := range protoc.Generators {
			protoc.Generators[i].Parameters = parameters[protoc.Generators[i].Name]
		}

		var (
			genDescriptors = shouldGenerateDescriptors(importDirPath)
			dfp            *os.File // tempfile for descriptors
		)

		if genDescriptors {
			dfp, err = ioutil.TempFile("", "descriptors.pb-")
			if err != nil {
				log.Fatalln(err)
			}
			protoc.Descriptors = dfp.Name()
		}

		arg, err := protoc.mkcmd()
		if err != nil {
			log.Fatalln(err)
		}

		if !quiet {
			fmt.Println(arg)
		}

		if dryRun {
			continue
		}

		if err := protoc.run(); err != nil {
			if quiet {
				log.Println(arg)
			}
			if err, ok := err.(*exec.ExitError); ok {
				if status, ok := err.Sys().(syscall.WaitStatus); ok {
					os.Exit(status.ExitStatus()) // proxy protoc exit status
				}
			}

			log.Fatalln(err)
		}

		if genDescriptors {
			desc, err := readDesc(protoc.Descriptors)
			if err != nil {
				log.Fatalln(err)
			}

			for path, set := range descriptorSets {
				if strings.HasPrefix(importDirPath, path) {
					set.add(desc.File...)
				}
			}
			descriptors = append(descriptors, desc)

			// clean up descriptors file
			if err := os.Remove(dfp.Name()); err != nil {
				log.Fatalln(err)
			}

			if err := dfp.Close(); err != nil {
				log.Fatalln(err)
			}
		}
	}

	for _, descriptorConfig := range c.Descriptors {
		fp, err := os.OpenFile(descriptorConfig.Target, os.O_TRUNC|os.O_WRONLY|os.O_CREATE, 0777)
		if err != nil {
			log.Fatalln(err)
		}
		defer fp.Sync()
		defer fp.Close()

		set := descriptorSets[descriptorConfig.Prefix]
		if len(set.merged.File) == 0 {
			continue // just skip if there is nothing.
		}

		if err := set.marshalTo(fp); err != nil {
			log.Fatalln(err)
		}
	}
}

func generators(names []string, outDir string) []generator {
	g := make([]generator, len(names))
	for i, name := range names {
		g[i].Name = name
		g[i].OutputDir = outDir
	}
	return g
}

type protoGoPkgInfo struct {
	Dir          string
	GoImportPath string
	ProtoFiles   []string
}

// goPkgInfo hunts down packages with proto files.
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

func gopaths() ([]string, error) {
	cmd := exec.Command("go", "env", "GOPATH")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	gp := strings.TrimSpace(string(out))

	if gp == "" {
		return nil, errors.New("go env GOPATH is empty")
	}

	return strings.Split(gp, string(filepath.ListSeparator)), nil
}

// gopathSrc modifies GOPATH elements from env to include the src directory.
func gopathSrc() (string, error) {
	gps, err := gopaths()
	if err != nil {
		return "", err
	}
	if len(gps) == 0 {
		return "", fmt.Errorf("must be run from a gopath")
	}
	var elements []string
	for _, element := range gps {
		elements = append(elements, filepath.Join(element, "src"))
	}

	return strings.Join(elements, string(filepath.ListSeparator)), nil
}

// gopathCurrent provides the top-level gopath for the current generation.
func gopathCurrent() (string, error) {
	gps, err := gopaths()
	if err != nil {
		return "", err
	}
	if len(gps) == 0 {
		return "", fmt.Errorf("must be run from a gopath")
	}

	return gps[0], nil
}

// gopathJoin combines the element with each path item and then recombines the set.
func gopathJoin(gopath, element string) string {
	gps := strings.Split(gopath, string(filepath.ListSeparator))
	var elements []string
	for _, p := range gps {
		elements = append(elements, filepath.Join(p, element))
	}

	return strings.Join(elements, string(filepath.ListSeparator))
}

// descriptorProto returns the full path to google/protobuf/descriptor.proto
// which might be different depending on whether it was installed. The argument
// is the list of paths to check.
func descriptorProto(paths []string) (string, string, error) {
	const descProto = "google/protobuf/descriptor.proto"

	for _, dir := range paths {
		file := path.Join(dir, descProto)
		if _, err := os.Stat(file); err == nil {
			return file, dir, err
		}
	}

	return "", "", fmt.Errorf("File %q not found (looked in: %v)", descProto, paths)
}

var errVendorNotFound = fmt.Errorf("no vendor dir found")

// closestVendorDir walks up from dir until it finds the vendor directory.
func closestVendorDir(dir string) (string, error) {
	dir = filepath.Clean(dir)
	for dir != filepath.Join(filepath.VolumeName(dir), string(filepath.Separator)) {
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

	return "", errVendorNotFound
}

func mergeMap(m1, m2 map[string]string) map[string]string {
	if m1 == nil {
		m1 = map[string]string{}
	}

	for k, v := range m2 {
		m1[k] = v
	}

	return m1
}
