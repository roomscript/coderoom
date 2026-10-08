# Session concepts

Session owns participant lifecycle and actual delivery. The interpreter owns
workflow coordination; the room projects conversation history.

| Concept | Meaning |
|---|---|
| Session | The runtime coordinating participants, policy, approvals, and delivery. |
| Participant | A named collaborator managed by session, backed by an agent adapter. |
| Participant send | A message requesting work from one primary participant in the room, with optional policy-driven notices to other participants. |
| Outside-room participant send | An outgoing message sent without a room record or notices to others. Agent responses can still appear in the room. |
| Broadcast | Work directed to a selected set of participants. |
| Notice | Context delivered without requesting substantive work. |
| Routing plan | Intended recipients frozen by session; it neither reserves readiness nor guarantees delivery. |
| Routing result | Actual delivery outcomes, including partial success; distinct from the routing plan. |
| Readiness requirements | Participants required to be ready before execution. Interpreter waits; session rechecks before delivery. Participants not ready are the unmet subset. |
| Handoff | Context transferred from one participant's completed room-visible output to another participant. |

## API vocabulary

- `SendToParticipantCommand`, `ParticipantSendPlan`, and
  `CreateParticipantSendPlan(alias)` describe an in-room participant send.
- `Message` requests work; `Notice` provides context to notice recipients.
- `SendToParticipantOutsideRoomCommand` omits the outgoing room record.
- `RequiredReadyAliases` identifies readiness requirements;
  `NotReadyAliases` identifies the currently unmet subset.
- `AgentReady` means initialization is complete and messages can be received.
  `CancelCommand` matches `/cancel alias`: interruption is asynchronous and the
  participant remains in the session.

`RoutingCompleted` reports every routing command's actual outcomes after its
attempts or rejection: recipient role, delivered/failed/not-attempted status,
and errors. Delivery means adapter acceptance; work completion is separate.
`HandoffDelivered` carries accepted context for room projection. Footers use
accepted recipients and distinguish failed or unsent recipients.
