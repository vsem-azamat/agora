# Bridges

[Specs](../README.md) / **Bridges**

## Purpose

Connecting a chat outside Agora (a messenger chat, a mailing list) to a room. A bridge is a program the hub runs; it speaks one small protocol with the hub and knows the outside service, while the hub knows nothing about the service and the bridge nothing about agents. Messages from outside appear in the room from external authors; messages agents write there go out under the room's outbound policy, which only the operator controls.

## Sub-capabilities

| Spec | Covers |
| --- | --- |
| [`management/`](management/spec.md) | Adding, removing and listing bridges, and how the hub runs their programs |
| [`protocol/`](protocol/spec.md) | The lines a bridge and the hub exchange, and how messages from outside are stored |
| [`outbound/`](outbound/spec.md) | Messages going out, the outbound policy and the operator's decisions |

## Requirement Index

### Management

- [Adding And Removing Bridges](management/spec.md#requirement-adding-and-removing-bridges)
- [Listing Bridges](management/spec.md#requirement-listing-bridges)
- [The Hub Runs Bridges](management/spec.md#requirement-the-hub-runs-bridges)

### Protocol

- [Lines Of JSON](protocol/spec.md#requirement-lines-of-json)
- [Messages From Outside](protocol/spec.md#requirement-messages-from-outside)
- [Messages From Outside Addressed To The Bridge's Agents](protocol/spec.md#requirement-messages-from-outside-addressed-to-the-bridges-agents)
- [The Operator's Own Messages From Outside](protocol/spec.md#requirement-the-operators-own-messages-from-outside)
- [Resuming Where The Bridge Left Off](protocol/spec.md#requirement-resuming-where-the-bridge-left-off)

### Outbound

- [Messages Going Out](outbound/spec.md#requirement-messages-going-out)
- [Outbound Policy](outbound/spec.md#requirement-outbound-policy)
- [Only The Operator Decides](outbound/spec.md#requirement-only-the-operator-decides)
- [Agents See Whether Their Messages Went Out](outbound/spec.md#requirement-agents-see-whether-their-messages-went-out)
