// Copyright 2016 Marapongo, Inc. All rights reserved.

// Package graphgen turns MuIL object creation, assignment, etc. events into a MuGL graph.
package graphgen

import (
	"github.com/marapongo/mu/pkg/compiler/core"
	"github.com/marapongo/mu/pkg/compiler/eval"
	"github.com/marapongo/mu/pkg/compiler/symbols"
	"github.com/marapongo/mu/pkg/compiler/types"
	"github.com/marapongo/mu/pkg/compiler/types/predef"
	"github.com/marapongo/mu/pkg/graph"
	"github.com/marapongo/mu/pkg/util/contract"
)

// Generator listens for events, records them as graph vertices and edges, and returns a DAG afterwards.
type Generator interface {
	eval.InterpreterHooks
	Graph() graph.Graph
}

// New allocates a fresh generator, ready to produce graphs.
func New(ctx *core.Context) Generator {
	return &generator{
		ctx: ctx,
		res: make(dependsSet),
	}
}

type generator struct {
	ctx *core.Context // the compiler context shared between passes.
	res dependsSet    // a full set of objects and their dependencies.
}

// objectSet is a set of object pointers; each entry has a ref-count to track how many occurrences it contains.
type objectSet map[*eval.Object]int

// dependsSet is a map of object pointers to the objectSet containing the set of objects each such object depends upon.
type dependsSet map[*eval.Object]objectSet

var _ Generator = (*generator)(nil)

// Graph takes the information recorded thus far and produces a new MuGL graph from it.
func (g *generator) Graph() graph.Graph {
	return nil
}

// OnNewObject is called whenever a new object has been allocated.
func (g *generator) OnNewObject(o *eval.Object) {
	contract.Assert(o != nil)
	// We only care about subclasses of the mu.Resource type; all others are "just" data/computation.
	if types.HasBaseName(o.Type(), predef.MuResourceClass) {
		// Add an entry to the depends set.  This should not already exist; it's the first time we encountered it.
		if _, has := g.res[o]; has {
			contract.Failf("Unexpected duplicate new object encountered")
		}
		g.res[o] = make(objectSet) // dependencies start out empty.
	}
}

// OnAssignProperty is called whenever a property has been (re)assigned; it receives both the old and new values.
func (g *generator) OnAssignProperty(o *eval.Object, prop string, old *eval.Object, nw *eval.Object) {
	contract.Assert(o != nil)

	// If the target of the assignment is a resource, we need to track dependencies.
	// TODO: if we are assigning to a structure inside of a structure inside... of a resource, we must also track.
	if types.HasBaseName(o.Type(), predef.MuResourceClass) {
		deps := g.res[o]

		// If the old object is a resource, drop a ref-count.
		if old != nil && types.HasBaseName(old.Type(), predef.MuResourceClass) {
			c, has := deps[old]
			contract.Assertf(has, "Expected old resource property to exist in dependency map")
			contract.Assertf(c > 0, "Expected old resource property ref-count to be > 0 in dependency map")
			deps[old]--
		}

		// If the new object is a resource, add a ref-count (or a whole new entry if needed).
		if nw != nil && types.HasBaseName(nw.Type(), predef.MuResourceClass) {
			if c, has := deps[nw]; has {
				deps[nw] = c + 1
			} else {
				deps[nw] = 1
			}
		}
	}
}

// OnEnterPackage is invoked whenever we enter a new package.
func (g *generator) OnEnterPackage(pkg *symbols.Package) {
}

// OnLeavePackage is invoked whenever we enter a new package.
func (g *generator) OnLeavePackage(pkg *symbols.Package) {
}

// OnEnterModule is invoked whenever we enter a new module.
func (g *generator) OnEnterModule(mod *symbols.Module) {
}

// OnLeaveModule is invoked whenever we enter a new module.
func (g *generator) OnLeaveModule(mod *symbols.Module) {
}

// OnEnterFunction is invoked whenever we enter a new function.
func (g *generator) OnEnterFunction(fnc symbols.Function) {
}

// OnLeaveFunction is invoked whenever we enter a new function.
func (g *generator) OnLeaveFunction(fnc symbols.Function) {
}
