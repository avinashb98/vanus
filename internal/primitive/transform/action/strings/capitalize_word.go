// Copyright 2023 Linkall Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package strings

import (
	"github.com/linkall-labs/vanus/internal/primitive/transform/action"
	"github.com/linkall-labs/vanus/internal/primitive/transform/arg"
	"github.com/linkall-labs/vanus/internal/primitive/transform/function"
)

// NewCapitalizeWord ["capitalize_word", "key"].
func NewCapitalizeWordAction() action.Action {
	a := &action.SourceTargetSameAction{}
	a.CommonAction = action.CommonAction{
		ActionName: "CAPITALIZE_WORD",
		FixedArgs:  []arg.TypeList{arg.EventList},
		Fn:         function.CapitalizeWord,
	}
	return a
}
