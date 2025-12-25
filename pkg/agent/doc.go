// agent is a specialized module which utilizes
// clai and the public models to create an agent.
//
// An agent is, in essence, a control loop calling LLM tools
// repeatedly after some trigger. The difference between agent A
// and agent B is the prompt, the model and the available tools.
//
// This package streamlines the creation of such agents
package agent
