package winapi

// Offline registry management API

type OrHKey uintptr

type RegType uint32

const (
	// Registry value types: https://docs.microsoft.com/en-us/windows/win32/sysinfo/registry-value-types
	REG_TYPE_NONE                       RegType = 0
	REG_TYPE_SZ                         RegType = 1
	REG_TYPE_EXPAND_SZ                  RegType = 2
	REG_TYPE_BINARY                     RegType = 3
	REG_TYPE_DWORD                      RegType = 4
	REG_TYPE_DWORD_LITTLE_ENDIAN        RegType = 4
	REG_TYPE_DWORD_BIG_ENDIAN           RegType = 5
	REG_TYPE_LINK                       RegType = 6
	REG_TYPE_MULTI_SZ                   RegType = 7
	REG_TYPE_RESOURCE_LIST              RegType = 8
	REG_TYPE_FULL_RESOURCE_DESCRIPTOR   RegType = 9
	REG_TYPE_RESOURCE_REQUIREMENTS_LIST RegType = 10
	REG_TYPE_QWORD                      RegType = 11
	REG_TYPE_QWORD_LITTLE_ENDIAN        RegType = 11
)

//sys OrMergeHives(hiveHandles []OrHKey, result *OrHKey) (win32err error) = offreg.ORMergeHives
//sys OrOpenHive(hivePath string, result *OrHKey) (win32err error) = offreg.OROpenHive
//sys OrCloseHive(handle OrHKey) (win32err error) = offreg.ORCloseHive
//sys OrSaveHive(handle OrHKey, hivePath string, osMajorVersion uint32, osMinorVersion uint32) (win32err error) = offreg.ORSaveHive
//sys OrOpenKey(handle OrHKey, subKey string, result *OrHKey) (win32err error) = offreg.OROpenKey
//sys OrCloseKey(handle OrHKey) (win32err error) = offreg.ORCloseKey
//sys OrCreateKey(handle OrHKey, subKey string, class uintptr, options uint32, securityDescriptor uintptr, result *OrHKey, disposition *uint32) (win32err error) = offreg.ORCreateKey
//sys OrDeleteKey(handle OrHKey, subKey string) (win32err error) = offreg.ORDeleteKey
//sys OrGetValue(handle OrHKey, subKey string, value string, valueType *uint32, data *byte, dataLen *uint32) (win32err error) = offreg.ORGetValue
//sys OrSetValue(handle OrHKey, valueName string, valueType uint32, data *byte, dataLen uint32) (win32err error) = offreg.ORSetValue
