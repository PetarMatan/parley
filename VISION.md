# Vision

**Every developer has an agent. The team still does not.**

Most code is now written with an agent. The conversation between developers did not move with it. A reviewer's agent writes the findings. The reviewer pastes them to the author. The author pastes them into their own agent. A person carries messages between two machines and adds nothing on the way.

Teams that keep carrying pay in days. Every review round is a trip through two calendars. Teams that let the agents talk pay in minutes, and their people talk only about what needs a person.

Parley is the second layer of the organization. Beside every person stands their agent. It speaks under that person's name, knows what that person knows, and argues the way that person decided. Agents settle with agents: the clear bug, the wrong finding, the name of a class. People settle with people: the product choice, the scope, the approval. Where two agents cannot agree is exactly where two people must. Parley brings them in there, and only there.

This is not two subagents told to argue. Behind each agent stands a different person with different knowledge and different decisions. That difference is the whole point.

It is not two agents watching a shared board or a merge request thread, either. That is where a team keeps its decisions. It is a poor place to make them. Text written there drives your agent with your full rights. Your colleague must read the whole agent conversation to find the decision. Parley keeps the agent conversation on its own channel. There, each message acts only as the person who sent it. Only the conclusion goes to the shared place.

---

This file states what Parley is for and the rules that follow from it. Read it before you choose a feature, a design, or a scope. When a choice conflicts with a rule here, either the choice changes or this file changes, and the change is recorded in `DECISIONS.md`.

Five words have one meaning in this file.

- A **person** is a member of the organization with a login.
- An **agent** is the coding agent that speaks for one person.
- The **channel** is Parley's agent-to-agent connection. It carries messages between two people's agents.
- The **record** is where the team keeps its work and its decisions: the issue tracker, the merge request, the wiki, the design document.
- An **agent conversation** is one exchange between two agents on the channel about one piece of work.

## 1. The premise

The product rests on one claim: **each person's agent is different because its person made it so.**

A person shapes their agent with standing instructions, memory, skills, the work they committed, the merge request they wrote, their role, and the context of the session they are in. Two people shape two different agents. When those two agents argue, two sets of knowledge and two sets of opinions meet.

Without this difference, two agents in a conversation are the same as one person running two subagents and telling them to argue. That is a useful tool, and it is not this product. The difference between the agents is the ground Parley stands on.

Parley assumes this difference. It does not create it and it does not store it (decision V1). What lives outside Parley:

- the person's instructions, memory, and skills
- the person's live session and what it knows
- the person's own work: commits, descriptions, tests, documents

What Parley provides:

- identity: every message names the person whose agent sent it, verified at the door
- delivery: a message reaches the other person's agent in its own session, live or queued
- safety: the receiving agent works inside limits a program enforces, and a program does the risky steps
- authority: a ceiling per person and per kind of change, set by people
- the log: every message on the channel and every conclusion, with the person who sent or decided it

## 2. The people stay

The conversation between people does not go away. It changes shape.

People decide direction, product behavior, scope, approval, and merge. Agents decide the rest inside the ceiling their person set: how to implement, where a method goes, what a class is called, whether a finding is right.

Before Parley, both kinds of decision mixed in one conversation between two people. Now the technical back-and-forth stays between the agents. The conversation between people is shorter and holds only what needs a person.

A service may take part as a sender of facts, for example a CI result. A service does not argue and it does not decide.

## 3. The agent conversation is not the record

One way to let two agents work together is to let them share the record. One agent writes a ticket. The other reacts to it. Each reads what the other wrote there. Parley does not do this.

The agent conversation runs on the channel. The technical part runs at the speed of agents. A decision for a person runs at the speed of that person, because an agent stops and waits when a point is above its limit. In both cases the agent conversation stays off the record. The record gets the result: the fix, the dispute and its reason, the decision a person made.

People read conclusions in the record. The log keeps the whole agent conversation for audit.

## 4. The test that can void this vision

If an agent no longer needs its person to be different from another person's agent, this design has no reason to exist in its present form. Agents would then hold enough knowledge on their own, and one agent could play both sides.

This is stated here so it can be checked, not avoided. While a person's input changes what their agent argues, the premise holds and the product has its reason.

## 5. Rules for every decision

Check a feature, a design, or a scope change against these rules.

1. **One agent, one person.** Every agent acts under one person's identity and authority. No shared agent, no anonymous agent, no agent that speaks for a team.
2. **The person's setup reaches their agent.** The quarantine strips secrets, network, and the rest of the repository. It must never strip the person's own instructions, memory, or skills. Without them the agent is not that person's agent.
3. **Parley holds the log, not the person.** Parley stores identities, the messages on the channel, and the conclusions. It does not build or hold a profile of a person. A change to this rule is a change to the product and goes through `DECISIONS.md`.
4. **Decisions move down, never up.** A feature may move a technical decision from a person to their agent, inside the ceiling. No feature moves direction, product, scope, approval, or merge from a person to an agent.
5. **Less noise for people, or nothing.** A feature that adds messages, prompts, or choices for a person must remove more than it adds. The measure of the product is how little a person must read to decide well.
6. **Controls outside the model.** The other person's message is prompt injection by design. Safety never depends on the model resisting it. Identity checks, typed schemas, the quarantine, the promoter, and the ceiling do the work.
7. **The channel is neutral.** Code review is the first kind of agent conversation, not the product. What belongs to one kind stays in that kind: schemas, context id, sender rule, authority policy, skills.
8. **Two today, many tomorrow.** The organization is a graph of people and their agents. No design may assume exactly two participants.
9. **The record holds conclusions, not the agent conversation.** The record gets what the agents concluded and what the people decided. The agent conversation stays on the channel.

## 6. Not in the vision

- Agents that act with no person behind them.
- Replacing a person's review, approval, or merge.
- A profile of a person held or built by Parley.
- A model that is trusted to resist a hostile message.
