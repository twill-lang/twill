package ir

import "fmt"

// Builder assembles a Graph. Every constructor infers the output shape from the
// operand shapes and returns an error where internal/tensor would return one,
// so a graph that builds is a graph whose shapes agree by construction and the
// backend never has to re-check them.
type Builder struct {
	g   Graph
	err error
}

// NewBuilder returns an empty builder.
func NewBuilder() *Builder { return &Builder{} }

// Err reports the first error the builder hit. Constructors return a Ref
// regardless so that a caller can build a whole expression and check once.
func (b *Builder) Err() error { return b.err }

func (b *Builder) fail(format string, args ...any) Ref {
	if b.err == nil {
		b.err = fmt.Errorf(format, args...)
	}
	return 0
}

func (b *Builder) add(n Node) Ref {
	b.g.Nodes = append(b.g.Nodes, n)
	return Ref(len(b.g.Nodes) - 1)
}

// raw appends an already-formed node. Only the gradient transform uses it, to
// replay a forward graph node for node without re-deriving shapes that are
// already known to be right.
func (b *Builder) raw(n Node) Ref { return b.add(n) }

// Shape reports the shape of a node already built.
func (b *Builder) Shape(r Ref) []int {
	if int(r) >= len(b.g.Nodes) {
		return nil
	}
	return b.g.Nodes[r].Shape
}

// Param declares a graph input and returns the node reading it.
func (b *Builder) Param(name string, shape []int) Ref {
	b.g.Params = append(b.g.Params, Param{Name: name, Shape: cloneShape(shape)})
	return b.add(Node{Op: OpParam, Shape: cloneShape(shape), Attr: Attr{Index: len(b.g.Params) - 1}})
}

// Const bakes a constant buffer into the graph.
func (b *Builder) Const(data []float64, shape []int) Ref {
	if len(data) != Numel(shape) {
		return b.fail("const: %d elements for shape %s", len(data), ShapeString(shape))
	}
	b.g.Consts = append(b.g.Consts, Const{Data: append([]float64(nil), data...), Shape: cloneShape(shape)})
	return b.add(Node{Op: OpConst, Shape: cloneShape(shape), Attr: Attr{Index: len(b.g.Consts) - 1}})
}

// Scalar is a rank-0 constant.
func (b *Builder) Scalar(x float64) Ref { return b.Const([]float64{x}, []int{}) }

// Unary builds a one-operand elementwise node.
func (b *Builder) Unary(op Op, x Ref) Ref {
	if op.Class() != ClassElementwise || op.Arity() != 1 {
		return b.fail("Unary: %s is not a unary elementwise op", op)
	}
	return b.add(Node{Op: op, In: []Ref{x}, Shape: cloneShape(b.Shape(x))})
}

// Clip builds clip(x, lo, hi).
func (b *Builder) Clip(x Ref, lo, hi float64) Ref {
	return b.add(Node{Op: OpClip, In: []Ref{x}, Shape: cloneShape(b.Shape(x)), Attr: Attr{F: lo, G: hi}})
}

// PowScalar builds x raised to a constant exponent, matching tensor.PowScalar,
// which is what twill's `^` operator lowers to (the interpreter refuses a
// non-scalar exponent).
func (b *Builder) PowScalar(x Ref, p float64) Ref {
	return b.add(Node{Op: OpPowScalar, In: []Ref{x}, Shape: cloneShape(b.Shape(x)), Attr: Attr{F: p}})
}

// Binary builds a two-operand broadcasting elementwise node.
func (b *Builder) Binary(op Op, x, y Ref) Ref {
	if op.Class() != ClassElementwise || op.Arity() != 2 {
		return b.fail("Binary: %s is not a binary elementwise op", op)
	}
	shape, ok := BroadcastShape(b.Shape(x), b.Shape(y))
	if !ok {
		return b.fail("shape mismatch: cannot broadcast %s with %s",
			ShapeString(b.Shape(x)), ShapeString(b.Shape(y)))
	}
	return b.add(Node{Op: op, In: []Ref{x, y}, Shape: shape})
}

// Where builds where(cond, a, b), broadcasting all three operands together.
func (b *Builder) Where(cond, x, y Ref) Ref {
	s1, ok := BroadcastShape(b.Shape(cond), b.Shape(x))
	if !ok {
		return b.fail("where: cannot broadcast %s with %s",
			ShapeString(b.Shape(cond)), ShapeString(b.Shape(x)))
	}
	shape, ok := BroadcastShape(s1, b.Shape(y))
	if !ok {
		return b.fail("where: cannot broadcast %s with %s", ShapeString(s1), ShapeString(b.Shape(y)))
	}
	return b.add(Node{Op: OpWhere, In: []Ref{cond, x, y}, Shape: shape})
}

// Sum reduces every element to a scalar.
func (b *Builder) Sum(x Ref) Ref {
	return b.add(Node{Op: OpSum, In: []Ref{x}, Shape: []int{}})
}

// Mean reduces every element to a scalar mean.
func (b *Builder) Mean(x Ref) Ref {
	return b.add(Node{Op: OpMean, In: []Ref{x}, Shape: []int{}})
}

func (b *Builder) reduceAxis(op Op, x Ref, axis int) Ref {
	in := b.Shape(x)
	// The message names the axis the caller wrote, the same way
	// tensor.normalizeAxis does. See the comment there.
	written := axis
	if axis < 0 {
		axis += len(in)
	}
	if axis < 0 || axis >= len(in) {
		return b.fail("%s: axis %d out of range for rank %d", op, written, len(in))
	}
	out := make([]int, 0, len(in)-1)
	out = append(out, in[:axis]...)
	out = append(out, in[axis+1:]...)
	return b.add(Node{Op: op, In: []Ref{x}, Shape: out, Attr: Attr{Axis: axis}})
}

// SumAxis sums along one axis, dropping it.
func (b *Builder) SumAxis(x Ref, axis int) Ref { return b.reduceAxis(OpSumAxis, x, axis) }

// MeanAxis averages along one axis, dropping it.
func (b *Builder) MeanAxis(x Ref, axis int) Ref { return b.reduceAxis(OpMeanAxis, x, axis) }

// SumTo sums a value back down to shape, which must broadcast up to the value's
// own shape. It is the inverse of a broadcast and exists for exactly one
// purpose: the VJP of a broadcasting elementwise op has to route the cotangent
// back to an operand that was smaller than the output.
func (b *Builder) SumTo(x Ref, shape []int) Ref {
	in := b.Shape(x)
	if shapeEqual(in, shape) {
		return x
	}
	up, ok := BroadcastShape(shape, in)
	if !ok || !shapeEqual(up, in) {
		return b.fail("sum_to: %s does not broadcast to %s", ShapeString(shape), ShapeString(in))
	}
	return b.add(Node{Op: OpSumTo, In: []Ref{x}, Shape: cloneShape(shape), Attr: Attr{Shape: cloneShape(shape)}})
}

// Reshape reinterprets the buffer under a new shape of the same element count.
func (b *Builder) Reshape(x Ref, shape []int) Ref {
	if Numel(b.Shape(x)) != Numel(shape) {
		return b.fail("reshape: %s has %d elements, %s wants %d",
			ShapeString(b.Shape(x)), Numel(b.Shape(x)), ShapeString(shape), Numel(shape))
	}
	return b.add(Node{Op: OpReshape, In: []Ref{x}, Shape: cloneShape(shape), Attr: Attr{Shape: cloneShape(shape)}})
}

// Transpose permutes axes.
func (b *Builder) Transpose(x Ref, perm []int) Ref {
	in := b.Shape(x)
	if len(perm) != len(in) {
		return b.fail("transpose: permutation of length %d for rank %d", len(perm), len(in))
	}
	seen := make([]bool, len(in))
	out := make([]int, len(in))
	for i, p := range perm {
		if p < 0 || p >= len(in) || seen[p] {
			return b.fail("transpose: %v is not a permutation of %d axes", perm, len(in))
		}
		seen[p] = true
		out[i] = in[p]
	}
	return b.add(Node{Op: OpTranspose, In: []Ref{x}, Shape: out, Attr: Attr{Shape: cloneShape(perm)}})
}

// BroadcastTo expands a value to a larger shape.
func (b *Builder) BroadcastTo(x Ref, shape []int) Ref {
	up, ok := BroadcastShape(b.Shape(x), shape)
	if !ok || !shapeEqual(up, shape) {
		return b.fail("broadcast_to: %s does not broadcast to %s",
			ShapeString(b.Shape(x)), ShapeString(shape))
	}
	if shapeEqual(b.Shape(x), shape) {
		return x
	}
	return b.add(Node{Op: OpBroadcastTo, In: []Ref{x}, Shape: cloneShape(shape), Attr: Attr{Shape: cloneShape(shape)}})
}

// MatMul builds a @ b with twill's 1-D promotion rules, matching
// tensor.MatMul: a 1-D left operand is a row, a 1-D right operand is a column,
// and the promoted dimension is dropped from the result.
func (b *Builder) MatMul(x, y Ref) Ref {
	as, bs := b.Shape(x), b.Shape(y)
	a2, b2 := as, bs
	if len(as) == 1 {
		a2 = []int{1, as[0]}
	}
	if len(bs) == 1 {
		b2 = []int{bs[0], 1}
	}
	if len(a2) != 2 || len(b2) != 2 {
		return b.fail("matmul requires 1-D or 2-D operands, got %s and %s",
			ShapeString(as), ShapeString(bs))
	}
	m, k := a2[0], a2[1]
	k2, n := b2[0], b2[1]
	if k != k2 {
		return b.fail("shape mismatch in matmul: %s @ %s (inner %d != %d)",
			ShapeString(as), ShapeString(bs), k, k2)
	}
	var out []int
	switch {
	case len(as) == 1 && len(bs) == 1:
		out = []int{}
	case len(as) == 1:
		out = []int{n}
	case len(bs) == 1:
		out = []int{m}
	default:
		out = []int{m, n}
	}
	return b.add(Node{Op: OpMatMul, In: []Ref{x, y}, Shape: out})
}

// Output marks a node as a graph result.
func (b *Builder) Output(r Ref) { b.g.Out = append(b.g.Out, r) }

// Finish validates and returns the graph.
func (b *Builder) Finish() (*Graph, error) {
	if b.err != nil {
		return nil, b.err
	}
	g := b.g
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return &g, nil
}

// Validate re-checks the structural invariants the builder is supposed to
// maintain: refs point backwards, arities match, shapes are non-negative, and
// every output exists. It is cheap and runs on every Finish, because a
// malformed graph reaching the emitter produces C that does not compile and a
// diagnostic nobody can read.
func (g *Graph) Validate() error {
	for i, n := range g.Nodes {
		if n.Op == OpInvalid || n.Op >= opCount {
			return fmt.Errorf("node %%%d: invalid op", i)
		}
		if a := n.Op.Arity(); a >= 0 && len(n.In) != a {
			return fmt.Errorf("node %%%d (%s): %d operands, want %d", i, n.Op, len(n.In), a)
		}
		for _, r := range n.In {
			if int(r) >= i || r < 0 {
				return fmt.Errorf("node %%%d (%s): operand %%%d is not defined before it", i, n.Op, r)
			}
		}
		for _, d := range n.Shape {
			if d < 0 {
				return fmt.Errorf("node %%%d (%s): negative dimension in %s", i, n.Op, ShapeString(n.Shape))
			}
		}
		switch n.Op {
		case OpParam:
			if n.Attr.Index >= len(g.Params) {
				return fmt.Errorf("node %%%d: param index %d out of range", i, n.Attr.Index)
			}
		case OpConst:
			if n.Attr.Index >= len(g.Consts) {
				return fmt.Errorf("node %%%d: const index %d out of range", i, n.Attr.Index)
			}
		}
	}
	for _, r := range g.Out {
		if int(r) >= len(g.Nodes) || r < 0 {
			return fmt.Errorf("output %%%d is not a node", r)
		}
	}
	return nil
}

// Uses counts, for every node, how many other nodes read it, plus one for each
// time it appears as a graph output. The fusion pass needs this: a value with
// more than one consumer has to be materialised or recomputed, and
// docs/CODEGEN.md section 3 says materialise first because it is the version
// that is obviously correct.
func (g *Graph) Uses() []int {
	uses := make([]int, len(g.Nodes))
	for _, n := range g.Nodes {
		for _, r := range n.In {
			uses[r]++
		}
	}
	for _, r := range g.Out {
		uses[r]++
	}
	return uses
}
