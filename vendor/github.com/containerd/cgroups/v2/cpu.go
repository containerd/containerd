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

package v2

type CPU struct {
	Weight *uint64
	Max    *uint64
	Cpus   string
	Mems   string
}

func (r *CPU) Values() (o []Value) {
	if r.Weight != nil {
		o = append(o, Value{
			filename: "cpu.weight",
			value:    *r.Weight,
		})
	}
	if r.Max != nil {
		o = append(o, Value{
			filename: "cpu.max",
			value:    *r.Max,
		})
	}
	if r.Cpus != "" {
		o = append(o, Value{
			filename: "cpuset.cpus",
			value:    r.Cpus,
		})
	}
	if r.Mems != "" {
		o = append(o, Value{
			filename: "cpuset.mems",
			value:    r.Mems,
		})
	}
	return o
}
