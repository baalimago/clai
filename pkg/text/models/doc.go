// Package models contains the public data structures used by the
// text package. These types are intentionally small and decoupled from
// internal representations so that they can remain stable for external
// consumers.
//
// The main entry points are:
//
//   - Chat:    a conversation consisting of ordered Messages.
//   - Message: a single chat message, optionally containing tool calls
//     or rich content such as images.
//   - Configurations: basic configuration parameters for constructing
//     text queriers.
//   - LLMTool, Specification, InputSchema, ParameterObject: types that
//     describe and carry calls to LLM tools, and that are compatible
//     with Model Context Protocol (MCP) style schemas.
//   - McpServer: describes an MCP server that can be registered and
//     used by the underlying text engine.
//
// These types are designed to be serializable (where appropriate) and
// safe to pass across package boundaries without leaking internal
// implementation details.
package models
