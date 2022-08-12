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

package commands

import (
   "fmt"
   "strings"
   "strconv"
)

// IntToInt32Array converts an array of int's to int32's
func IntToInt32Array(in []int) []int32 {
	var ret []int32

	for _, v := range in {
		ret = append(ret, int32(v))
	}
	return ret
}

// TraverseMap walks the depth of a map and returns the value of the nested keys provided.
func TraverseMap(m map[string]interface{}, keys ...string) (value interface{}, err error) {

   var (
      ok bool
      k []interface{}
   )

   if len(keys) == 0 {
      return nil, fmt.Errorf("TraverseMap needs at least one key")
   }
   if strings.Contains(keys[0], "[") && strings.Contains(keys[0], "]") { // handling arrays
      s1 := strings.Split(keys[0], "[")
      if len(s1) != 2 {
         return nil, fmt.Errorf("invalid key %v", keys[0])
      }
      if value, ok = m[s1[0]]; !ok {
         return nil, fmt.Errorf("key not found; remaining keys: %v", keys)
      } else if k, ok = value.([]interface{}); !ok {
         return nil, fmt.Errorf("malformed structure at %#v", value)
      } else {
         s2 := strings.Split(s1[1], "]")
         if s2[0] == "%" {
            var arr []interface{}
            for i, _ := range k {
               a, err := TraverseMap(m, append([]string{s1[0] + "["+strconv.Itoa(i)+"]"}, keys[1:]...)...)
               if err != nil {
                  return nil, err
               }

               arr = append(arr, a)
            }

            return arr, nil
         }
         index, err := strconv.Atoi(s2[0])
         if err != nil {
            return nil, fmt.Errorf("invalid key %v", keys[0])
         }
         value = k[index]
      }
   } else {
      if value, ok = m[keys[0]]; !ok {
         return nil, fmt.Errorf("key not found; remaining keys: %v", keys)
      }
   }

   if len(keys) == 1 { // Final key
      return value, nil
   } else if m, ok = value.(map[string]interface{}); !ok {
      return nil, fmt.Errorf("malformed structure at %#v", value)
   } else {
      return TraverseMap(m, keys[1:]...)
   }
}