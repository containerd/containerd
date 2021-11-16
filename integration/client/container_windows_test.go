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

package client

import (
	"golang.org/x/sys/windows"
)

// grantReadToEveryone adds a new entry in the ACL
// of the file or folder, allowing "Everyone" to read
// the contents. Inherit is set for all objects and
// containers.
func grantReadToEveryone(file string) error {
	secInfo, err := windows.GetNamedSecurityInfo(
		file, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)

	if err != nil {
		return err
	}
	dacl, _, err := secInfo.DACL()
	if err != nil {
		return err
	}
	// create a *SID for "Everyone"
	sid, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return err
	}

	// Construct the explicit access
	access := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}

	// Extend existing DACL to include GENERIC_READ for "Everyone"
	newDacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{access}, dacl)
	if err != nil {
		return err
	}

	// Set new DACL on the volume mount source.
	if err := windows.SetNamedSecurityInfo(
		file, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil, nil, newDacl, nil); err != nil {

		return err
	}
	return nil
}
