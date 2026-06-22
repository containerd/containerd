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
	"fmt"
	"log"
	"os"

	"golang.org/x/sys/windows"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: get_owner_windows.exe file_or_directory")
		os.Exit(1)
	}

	if _, err := os.Stat(os.Args[1]); err != nil {
		log.Fatal(err)
	}

	secInfo, err := windows.GetNamedSecurityInfo(
		os.Args[1], windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)

	if err != nil {
		log.Fatal(err)
	}
	sid, _, err := secInfo.Owner()
	if err != nil {
		log.Fatal(err)
	}
	acct, _, _, err := sid.LookupAccount(".")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s:%s", acct, sid)
}
