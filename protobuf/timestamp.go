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

package protobuf

import (
	"time"

	"github.com/gogo/protobuf/types"
)

// Once we migrate off from gogo/protobuf, we can use the function below, which don't return any errors.
// https://github.com/protocolbuffers/protobuf-go/blob/v1.28.0/types/known/timestamppb/timestamp.pb.go#L200-L208

// ToTimestamp creates protobuf's Timestamp from time.Time.
func ToTimestamp(from time.Time) *types.Timestamp {
	pt, err := types.TimestampProto(from)
	if err != nil {
		panic(err)
	}
	return pt
}

// FromTimestamp creates time.Time from protobuf's Timestamp.
func FromTimestamp(from *types.Timestamp) time.Time {
	if from == nil {
		// Return time.Time's zero value as like timestamppb.
		return time.Time{}.UTC()
	}
	tt, err := types.TimestampFromProto(from)
	if err != nil {
		panic(err)
	}
	return tt
}
