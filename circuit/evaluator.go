//
// evaluator.go
//
// Copyright (c) 2019-2023 Markku Rossi
//
// All rights reserved.
//

package circuit

import (
	"fmt"
	"math/big"

	"github.com/markkurossi/mpc/ot"
	"github.com/markkurossi/mpc/p2p"
)

var (
	debug = false
)

// Evaluator runs the evaluator on the P2P network.
func Evaluator(conn *p2p.Conn, oti ot.OT, circ *Circuit, inputs *big.Int,
	verbose bool) ([]*big.Int, error) {

	timing := NewTiming()

	garbled := make([][]ot.Label, circ.NumGates)

	// Receive program info.
	if verbose {
		fmt.Printf(" - Waiting for circuit info...\n")
	}
	key, err := conn.ReceiveData()
	if err != nil {
		return nil, err
	}

	// Receive garbled tables.
	timing.Sample("Wait", nil)
	if verbose {
		fmt.Printf(" - Receiving garbled circuit...\n")
	}
	count, err := conn.ReceiveUint32()
	if err != nil {
		return nil, err
	}
	if count != circ.NumGates {
		return nil, fmt.Errorf("wrong number of gates: got %d, expected %d",
			count, circ.NumGates)
	}
	var label ot.Label
	var labelData ot.LabelData
	for i := 0; i < circ.NumGates; i++ {
		count, err := conn.ReceiveUint32()
		if err != nil {
			return nil, err
		}

		values := make([]ot.Label, count)
		for j := 0; j < count; j++ {
			err := conn.ReceiveLabel(&label, &labelData)
			if err != nil {
				return nil, err
			}
			values[j] = label
		}
		garbled[i] = values
	}

	wires := make([]ot.Label, circ.NumWires)

	// Receive peer inputs.
	for i := 0; i < circ.Inputs[0].Size; i++ {
		err := conn.ReceiveLabel(&label, &labelData)
		if err != nil {
			return nil, err
		}
		wires[Wire(i)] = label
	}

	// Init oblivious transfer.
	err = oti.InitReceiver(conn)
	if err != nil {
		return nil, err
	}
	ioStats := conn.Stats
	timing.Sample("Recv", []string{FileSize(ioStats.Sum()).String()})

	// Query our inputs.
	if verbose {
		fmt.Printf(" - Querying our inputs...\n")
	}
	// Wire offset.
	if err := conn.SendUint32(circ.Inputs[0].Size); err != nil {
		return nil, err
	}
	// Wire count.
	if err := conn.SendUint32(circ.Inputs[1].Size); err != nil {
		return nil, err
	}
	if err := conn.Flush(); err != nil {
		return nil, err
	}
	flags := make([]bool, circ.Inputs[1].Size)
	for i := 0; i < circ.Inputs[1].Size; i++ {
		if inputs.Bit(i) == 1 {
			flags[i] = true
		}
	}
	if err := oti.Receive(flags, wires[circ.Inputs[0].Size:]); err != nil {
		return nil, err
	}
	xfer := conn.Stats.Sub(ioStats)
	ioStats = conn.Stats
	timing.Sample("Inputs", []string{FileSize(xfer.Sum()).String()})

	// Evaluate gates.
	if verbose {
		fmt.Printf(" - Evaluating circuit...\n")
	}
	err = circ.Eval(key[:], wires, garbled)
	if err != nil {
		return nil, err
	}
	timing.Sample("Eval", nil)

	// Resolve result values.

	var labels []ot.Label

	for i := 0; i < circ.Outputs.Size(); i++ {
		r := wires[Wire(circ.NumWires-circ.Outputs.Size()+i)]
		labels = append(labels, r)
	}
	for _, l := range labels {
		if err := conn.SendLabel(l, &labelData); err != nil {
			return nil, err
		}
	}
	if err := conn.Flush(); err != nil {
		return nil, err
	}

	result, err := conn.ReceiveData()
	if err != nil {
		return nil, err
	}
	raw := big.NewInt(0).SetBytes(result)

	xfer = conn.Stats.Sub(ioStats)
	ioStats = conn.Stats
	timing.Sample("Result", []string{FileSize(xfer.Sum()).String()})
	if verbose {
		timing.Print(conn.Stats.Sent.Load(), conn.Stats.Recvd.Load())
	}

	return circ.Outputs.Split(raw), nil
}
