package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"text/template"

	"github.com/yungene/pifra"
)

var exists = struct{}{}

// States is a set of states.
type States map[int]struct{}

// Actions maps labels to their list of transitions.
type Actions map[pifra.Label][]pifra.Transition

var blockIDCounter int

// Block is a set of states, identified by a unique integer.
type Block struct {
	id     int
	states States
}

// Blocks is a set of Blocks keyed by their IDs.
type Blocks map[int]Block

// StateBlocks map State IDs to their containing Block for fast lookup.
type StateBlocks map[int]Block

// Partition is primarily a set of Blocks, but also carries some auxiliary data
// to simplify and optimise the implementation.
type Partition struct {
	blocks  Blocks
	states  StateBlocks
	actions Actions
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func closeFile(f *os.File) {
	check(f.Close())
}

func decodeLTS(name string) (lts pifra.Lts, err error) {
	file, err := os.Open(name)
	if err != nil {
		return
	}
	defer closeFile(file)
	dec := gob.NewDecoder(file)
	err = dec.Decode(&lts)
	return
}

func uniquifyLTS(lts *pifra.Lts, right bool) {
	var offset int
	if right {
		offset = 1
	}
	uniquify := func(id int) int {
		return (id * 2) + offset
	}
	states := make(map[int]pifra.Configuration, len(lts.States))
	for id, conf := range lts.States {
		states[uniquify(id)] = conf
	}
	lts.States = states
	for i, trans := range lts.Transitions {
		lts.Transitions[i].Source = uniquify(trans.Source)
		lts.Transitions[i].Destination = uniquify(trans.Destination)
	}
}

func newBlock() Block {
	b := Block{id: blockIDCounter, states: make(States)}
	blockIDCounter++
	return b
}

func collectStates(part Partition, block Block, lts pifra.Lts) {
	for state := range lts.States {
		block.states[state] = exists
		part.states[state] = block
	}
}

func collectActions(part Partition, lts pifra.Lts) {
	for _, trans := range lts.Transitions {
		if slice, ok := part.actions[trans.Label]; ok {
			part.actions[trans.Label] = append(slice, trans)
		} else {
			part.actions[trans.Label] = []pifra.Transition{trans}
		}
	}
}

func (bs Blocks) add(b Block) {
	bs[b.id] = b
}

func (bs Blocks) remove(b Block) {
	delete(bs, b.id)
}

func newPartition(left, right pifra.Lts) Partition {
	part := Partition{
		blocks:  make(Blocks),
		states:  make(StateBlocks),
		actions: make(Actions),
	}
	block := newBlock()
	part.blocks.add(block)
	collectStates(part, block, left)
	collectStates(part, block, right)
	collectActions(part, left)
	collectActions(part, right)
	return part
}

func destinations(source int, action pifra.Label, part Partition) []int {
	dests := make(Blocks)
	for _, trans := range part.actions[action] {
		if trans.Label == action && trans.Source == source {
			dests.add(part.states[trans.Destination])
		}
	}
	ids := make([]int, len(dests))
	for id := range dests {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i, x := range a {
		if b[i] != x {
			return false
		}
	}
	return true
}

func splitKS(block Block, action pifra.Label, part Partition) (Block, Block) {
	var s int
	for state := range block.states {
		s = state
		break
	}
	b1 := newBlock()
	b2 := newBlock()
	sdests := destinations(s, action, part)
	for t := range block.states {
		tdests := destinations(t, action, part)
		if equalInts(sdests, tdests) {
			b1.states[t] = exists
		} else {
			b2.states[t] = exists
		}
	}
	if len(b2.states) == 0 {
		return block, b2
	}
	return b1, b2
}

func refine(part Partition, b, b1, b2 Block) {
	part.blocks.remove(b)
	part.blocks.add(b1)
	part.blocks.add(b2)
	for state := range b.states {
		if _, ok := b1.states[state]; ok {
			part.states[state] = b1
		} else {
			part.states[state] = b2
		}
	}
}

func partKS(left, right pifra.Lts) Partition {
	part := newPartition(left, right)
	changed := true
	for changed {
		changed = false
	out:
		for id, block := range part.blocks {
			for action := range part.actions {
				b1, b2 := splitKS(block, action, part)
				if b1.id == id {
					continue
				}
				refine(part, block, b1, b2)
				changed = true
				break out
			}
		}
	}
	return part
}

func isLeft(state int) bool {
	return state%2 == 0
}

func (ss States) bisimilar() bool {
	var left, right bool
	for s := range ss {
		if isLeft(s) {
			left = true
		} else {
			right = true
		}
		if left && right {
			return true
		}
	}
	return false
}

type Bisimulation map[int]int

func (p Partition) bisimilar() Bisimulation {
	var label int
	bisim := make(Bisimulation)
	for _, block := range p.blocks {
		if !block.states.bisimilar() {
			return nil
		}
		for state := range block.states {
			bisim[state] = label
		}
		label++
	}
	return bisim
}

func bisimGraphViz(bisim Bisimulation, lts pifra.Lts) []byte {
	var buf bytes.Buffer
	type StateTmpl struct {
		Label int
		Attrs string
	}
	type TransTmpl struct {
		Src   int
		Dest  int
		Label string
	}
	const stmpl = "    {{.Label}} [{{.Attrs}}label=\"{{.Label}}\"]\n"
	const ttmpl = "    {{.Src}} -> {{.Dest}} [label=\"{{ .Label}}\"]\n"
	stateTmpl := template.Must(template.New("state").Parse(stmpl))
	transTmpl := template.Must(template.New("trans").Parse(ttmpl))

	states := make([]int, len(lts.States))
	for state := range lts.States {
		states = append(states, state)
	}
	sort.Ints(states)

	buf.WriteString("digraph {\n")
	for _, state := range states {
		label := bisim[state]
		var attrs string
		if lts.RegSizeReached[state] {
			attrs += "peripheries=3,"
		} else if state == 0 || state == 1 {
			attrs += "peripheries=2,"
		}
		node := StateTmpl{Label: label, Attrs: attrs}
		stateTmpl.Execute(&buf, node)
	}
	buf.WriteRune('\n')
	for _, trans := range lts.Transitions {
		transTmpl.Execute(&buf, TransTmpl{
			Src:   bisim[trans.Source],
			Dest:  bisim[trans.Destination],
			Label: trans.Label.PrettyPrintGraph(),
		})
	}
	buf.WriteString("}\n")
	return buf.Bytes()
}

func writeFile(name string, data []byte) error {
	dir := filepath.Dir(name)
	os.MkdirAll(dir, os.ModePerm)
	return ioutil.WriteFile(name, data, 0644)
}

func init() {
	pifra.RegisterGobs()
}

func main() {
	if len(os.Args) < 4 {
		log.Fatalln("Wrong number of arguments")
	}
	left, err := decodeLTS(os.Args[1])
	check(err)
	right, err := decodeLTS(os.Args[2])
	check(err)
	uniquifyLTS(&left, false)
	uniquifyLTS(&right, true)
	part := partKS(left, right)
	bisim := part.bisimilar()
	if bisim == nil {
		fmt.Println("Not bisimilar")
		os.Exit(1)
	}
	data := bisimGraphViz(bisim, left)
	check(writeFile(os.Args[3]+"-left.dot", data))
	data = bisimGraphViz(bisim, right)
	check(writeFile(os.Args[3]+"-right.dot", data))
}
