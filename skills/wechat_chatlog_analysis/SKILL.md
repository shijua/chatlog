# WeChat Chatlog Analysis

## Resolve identity before reading content

- Join every message on the sender account id, never on a display name.

- Build an identity map first with remark name, nickname, alias and every group nickname for each account id.

- List group senders who are not in the contact list as unresolved and do not guess who they are.

- Never merge two account ids automatically, even when the names look alike, without message level proof such as a self introduction or a system message that names the account id.

- Treat a private chat name, a group nickname and a system message name as three different forms of the same person only after the account id proves the link.

## Standing exclusion rule (user-fixed, 2026-11-01)

- In group chats, that person's messages as sender are still left out of the narrative (use `exclude_sender` with the same account id), unless the user later explicitly says to remove only the private chat.
- Never re-add the excluded private chat by topic selection, and never let a later merge or monthly update bring it back.

## Build one unified index

- Prefer the whole account timeline endpoint when it exists, because it returns every message table in chronological order in one sweep, with no talker parameter and no session list in between.

- Query it by a fixed time window with an exclusive end, in Beijing time, and page with its cursor until there is no next page, keeping the same start and end for every page.

- Use the pair of source plus local message id as the deduplication key, never a content hash alone, so same second duplicates across tables are kept and edited messages are not double counted.

- Treat hidden chats, unmapped tables and same named tables in different databases as in scope by default, because the timeline endpoint enumerates message tables directly instead of going through sessions.

- Fall back to private chats from local export files and group chats from an API export that carries per message sender ids only when the timeline endpoint is unavailable.

- Do not use a local group file for sender attribution when it carries no per message sender names.

- Keep only time, talker, sender account id, message type, content hash and text content in the index, plus source, local message id and identity resolved flag when the timeline endpoint supplies them.

- Deduplicate forwards, copy paste and sticker floods by content hash plus time proximity before counting them as separate events, after the source plus local id deduplication above.

- Record the baseline index and its hash so a later incremental run can diff against it instead of rescanning everything, and for timeline pagination fix the window end and overlap the previous window, because a live import during paging can otherwise move a message before the cursor.

## Find bursts first, then events

- Compute daily volume per talker and per group and flag a day when volume exceeds about three times the median or when three high days run in a row.

- Treat a burst as a candidate only, never as an event on its own, because jokes and floods also burst.

- Use time proximity as the primary gate for clustering, not rare word overlap, because rare word overlap misses events where different chats paraphrase the same thing, mention it indirectly, or use images and emoji instead of the same words.

- Define time proximity as within three days for a single event and within fourteen days for a process such as an application, a move or a relationship change.

- Form a candidate cluster from everything in the same time window first, then score it with weights instead of rejecting it for a missing link.

- Weight a message by how much the user actually took part: a private chat and a group where the user actively sends count at full weight, while a lurking group where the user sends under one percent of messages counts at about one fifth weight, so a lurking only burst reads as background context and never as a personal event on its own.

- Add weight for participant overlap, defined as two or more shared account ids across chats, or a person mentioned in one chat who sends in another.

- Add weight for rare word overlap, defined as shared place names, person names, exam names, company names or concrete objects, but never treat its absence as a rejection, and never count common words such as laughter, eating or games.

- Split one time window into two events when the participant sets are disjoint and the topics clearly differ, even when the dates overlap, because same date does not mean same event.

## Verify every event against original messages

- Read about twenty messages of context around each candidate before writing it up.

- Check whether the chats describe one event or the same people talking about two things at once.

- Check whether the event belongs to the user, to a friend, or to forwarded news in the group.

- Never turn a keyword count into a relationship judgement, because a word count does not show who said it, to whom, or whether it was a joke.

- Mark what is unknown as unknown and do not fill in a cause, a place or a companion list that the original messages do not state.

## Write the timeline by time

- Write the main timeline in chronological order by month and by day, not by talker and not by chat.

- Weave private chats, group chats and hidden chats into the same day when they happened on the same day, so one day reads as one story.

- Keep subtitles time based and event based, never person based, in the timeline part.

- Write each section as a content summary plus analysis: first what happened, then why it matters and how it connects to earlier and later events.

- Use data only as background for finding peaks, and do not pile counts, keyword counts or daily volume lists into the narrative.

- Quote only a few short dated excerpts as evidence and never paste a long private or emotional message in full.

- Map chat evidence to inferred real life plans where useful, label each inference as an inference, and show which messages support it.

- Put the relationship analysis at the very end as its own part, written naturally as character arcs over time, not as a list of counts.

## Write the relationship analysis (Part 2)

- Part 2 is a required deliverable, not optional: after the chronological timeline, write one section per person, in the same natural narrative style as the timeline, covering how that relationship started, how it changed over time, and where it stands.
- Each person's section keeps the factual narrative first, then adds one explicitly labelled inference paragraph: that person's place in the user's life, the basis for closeness and trust, why the relationship changed, and what the change means. Where evidence is insufficient, write that it cannot be read from the records and do not attribute a cause.
- Use group-chat evidence as well as private-chat evidence in each person's arc, and note where private and group behaviour are consistent or differ. Do not base a relationship judgement on a word count alone.
- Keep Part 2 last: nothing is appended after it, and a later timeline update only revises the arcs that actually changed.

- Integrate later additions seamlessly into the existing months and never leave labels such as supplement, batch or draft in the delivered file.

- Keep unresolved items in an internal file only, never as a candidate section in the delivered timeline.

## Check repeatedly with independent agents

- Run the work through subagents for parallel reading and writing, with the main agent managing progress, quality and delivery.

- After each batch, run an independent check agent, fix what it finds, and check again.

- Check identity merges, absolute claims, sensitive identifiers, duplicate sections, chronological order and whether the relationship part is still last.

- Report progress at milestones, report when a new agent opens, and report at once when work stalls or stops.

- Never present an aggregate count as proof that an original message was read, and never claim a full read when only peaks and samples were read.

## Available connectors

- Use `muse.connector_search` to find other connectors and `muse.skill_search` to find related skills.

- If nothing here fits, look on the web for a maintained API or MCP server and set it up with [Custom Connectors and MCPs](/opt/muse/skills/custom-connector-setup/SKILL.md), or build a custom connector.
