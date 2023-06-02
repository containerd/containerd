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
	"go/ast"
	"regexp"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

func compilePattern(c config) (*regexp.Regexp, error) {
	return regexp.Compile(strings.Join(c.acronyms, "|"))
}

func rewriteNode(pattern *regexp.Regexp, node ast.Node) {
	astutil.Apply(
		node,
		func(c *astutil.Cursor) bool {
			node := c.Node()
			ident, ok := node.(*ast.Ident)
			if !ok {
				return true
			}

			matches := pattern.FindAllStringSubmatchIndex(ident.Name, -1)
			if matches == nil {
				return true
			}

			var (
				result string
				copied int
			)
			for _, match := range matches {
				// If there are submatches, ignore the first element and only process the submatches.
				if len(match) > 2 {
					match = match[2:]
				}

				for i := 0; i < len(match); i += 2 {
					if match[i] == -1 {
						continue
					}
					result += ident.Name[copied:match[i]]
					result += strings.ToUpper(ident.Name[match[i]:match[i+1]])
					copied = match[i+1]
				}
			}
			// Copy the rest.
			result += ident.Name[copied:len(ident.Name)]
			ident.Name = result
			return false
		},
		nil,
	)
}
