// Package flow is a lightweight flow-orchestration engine, ported from Solon-Flow
// (https://solon.noear.org/article/learn-solon-flow) to idiomatic Go.
//
// A Graph is built from Nodes (points) and Links (lines). The engine evaluates a
// graph starting from its start node, following outgoing links (whose when-conditions
// hold) and running each node's task. This file holds the graph model: NodeType,
// Node/NodeSpec, Link/LinkSpec, Graph/GraphSpec, and the config parser (FromText).
//
// Execution (Engine/Driver/Context/Evaluation) is added in later phases; P0-a covers
// only the immutable model + YAML/JSON parsing. See docs/arch/flow/ for the design.
package flow
