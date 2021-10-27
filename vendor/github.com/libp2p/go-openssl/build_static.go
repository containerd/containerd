// Copyright (C) 2017. See AUTHORS.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// +build openssl_static

package openssl

// #cgo linux windows freebsd openbsd solaris pkg-config: --static libssl libcrypto
// #cgo linux freebsd openbsd solaris CFLAGS: -Wno-deprecated-declarations
// #cgo darwin CFLAGS: -I/usr/local/opt/openssl@1.1/include -I/usr/local/opt/openssl/include -Wno-deprecated-declarations
// #cgo darwin LDFLAGS: -L/usr/local/opt/openssl@1.1/lib -L/usr/local/opt/openssl/lib -lssl -lcrypto
// #cgo windows CFLAGS: -DWIN32_LEAN_AND_MEAN
import "C"
