// Copyright 2024 The Erigon Authors
// This file is part of Erigon.
//
// Erigon is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// Erigon is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with Erigon. If not, see <http://www.gnu.org/licenses/>.

package jsonrpc

import (
	"context"
	"math/big"

	"github.com/holiman/uint256"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/common/hexutil"
	"github.com/erigontech/erigon/core/vm"
)

func (api *OtterscanAPIImpl) TraceTransaction(ctx context.Context, hash common.Hash) ([]*TraceEntry, error) {
	tx, err := api.db.BeginTemporalRo(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	tracer := NewTransactionTracer(ctx)
	if _, err := api.runTracer(ctx, tx, hash, tracer); err != nil {
		return nil, err
	}

	return tracer.Results, nil
}

type TraceEntry struct {
	Type   string         `json:"type"`
	Depth  int            `json:"depth"`
	From   common.Address `json:"from"`
	To     common.Address `json:"to"`
	Value  *hexutil.Big   `json:"value"`
	Input  hexutil.Bytes  `json:"input"`
	Output hexutil.Bytes  `json:"output"`
}

type TransactionTracer struct {
	DefaultTracer
	ctx     context.Context
	Results []*TraceEntry
	depth   int // computed from CaptureStart, CaptureEnter, and CaptureExit calls
	stack   []*TraceEntry
}

func NewTransactionTracer(ctx context.Context) *TransactionTracer {
	return &TransactionTracer{
		ctx:     ctx,
		Results: make([]*TraceEntry, 0),
		stack:   make([]*TraceEntry, 0),
	}
}

func (t *TransactionTracer) captureStartOrEnter(typ vm.OpCode, from, to common.Address, precompile bool, input []byte, value *uint256.Int) {
	inputCopy := make([]byte, len(input))
	copy(inputCopy, input)
	_value := new(big.Int)
	if value != nil {
		_value.Set(value.ToBig())
	}

	var entry *TraceEntry
	if typ == vm.CALL {
		entry = &TraceEntry{"CALL", t.depth, from, to, (*hexutil.Big)(_value), inputCopy, nil}
	} else if typ == vm.STATICCALL {
		entry = &TraceEntry{"STATICCALL", t.depth, from, to, nil, inputCopy, nil}
	} else if typ == vm.DELEGATECALL {
		entry = &TraceEntry{"DELEGATECALL", t.depth, from, to, nil, inputCopy, nil}
	} else if typ == vm.CALLCODE {
		entry = &TraceEntry{"CALLCODE", t.depth, from, to, (*hexutil.Big)(_value), inputCopy, nil}
	} else if typ == vm.CREATE {
		entry = &TraceEntry{"CREATE", t.depth, from, to, (*hexutil.Big)(value.ToBig()), inputCopy, nil}
	} else if typ == vm.CREATE2 {
		entry = &TraceEntry{"CREATE2", t.depth, from, to, (*hexutil.Big)(value.ToBig()), inputCopy, nil}
	} else if typ == vm.SELFDESTRUCT {
		last := t.Results[len(t.Results)-1]
		entry = &TraceEntry{"SELFDESTRUCT", last.Depth + 1, from, to, (*hexutil.Big)(value.ToBig()), nil, nil}
	} else {
		// safeguard in case new CALL-like opcodes are introduced but not handled,
		// otherwise CaptureExit/stack will get out of sync
		entry = &TraceEntry{"UNKNOWN", t.depth, from, to, (*hexutil.Big)(value.ToBig()), inputCopy, nil}
	}

	// Ignore precompiles in the returned trace (maybe we shouldn't?)
	if !precompile {
		t.Results = append(t.Results, entry)
	}

	// stack precompiles in order to match captureEndOrExit
	t.stack = append(t.stack, entry)
}

func (t *TransactionTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, precompile bool, create bool, input []byte, gas uint64, value *uint256.Int, code []byte) {
	t.depth = 0
	t.captureStartOrEnter(vm.CALL, from, to, precompile, input, value)
}

func (t *TransactionTracer) CaptureEnter(typ vm.OpCode, from common.Address, to common.Address, precompile bool, create bool, input []byte, gas uint64, value *uint256.Int, code []byte) {
	t.depth++
	t.captureStartOrEnter(typ, from, to, precompile, input, value)
}

func (t *TransactionTracer) captureEndOrExit(output []byte, usedGas uint64, err error) {
	t.depth--

	lastIdx := len(t.stack) - 1
	pop := t.stack[lastIdx]
	t.stack = t.stack[:lastIdx]

	outputCopy := make([]byte, len(output))
	copy(outputCopy, output)
	pop.Output = outputCopy
}

func (t *TransactionTracer) CaptureExit(output []byte, usedGas uint64, err error) {
	t.captureEndOrExit(output, usedGas, err)
}

func (t *TransactionTracer) CaptureEnd(output []byte, usedGas uint64, err error) {
	t.captureEndOrExit(output, usedGas, err)
}
