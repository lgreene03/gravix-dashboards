# Antigravity Orchestrator Loop for Gravix

The Main Antigravity Agent operates as a router and state manager, executing tasks by delegating to specialized subagents and tracking status on a central Blackboard.

---

## 1. Central Blackboard (State Management)

Maintain a temporary JSON state object for the duration of the task. Do not pass the entire conversation history to subagents.

```json
{
  "trace_id": "gravix-task-12345",
  "task_goal": "Implement Stripe Billing Integration",
  "phase": 1,
  "modified_files": [],
  "known_context": "Durable ingestion sink is working, SQLite tenant tables present",
  "current_status": "in_progress"
}
```

---

## 2. Tools as Agents (Function Calling)

Expose subagents as explicitly defined tools to the Main Agent. The Main Agent routes work by calling these tools.

- `call_cpo(request)` -> Returns a validated execution plan or rejects the request based on product rules in `AGENTS.md`.
- `call_senior_engineering_lead(blackboard_state)` -> Returns technical specification and sprint plan JSON.
- `call_senior_engineer(blackboard_state)` -> Executes implementation, writes Go code and tests, and returns JSON result.

---

## 3. Execution Flow

1. **Receive Request:** User provides a goal.
2. **Triage (Delegate):** Call `call_cpo(goal)`. Wait for product alignment.
3. **Architect:** Call `call_senior_engineering_lead(blackboard_state)`. Get technical spec and sprint plan.
4. **Execute:**
   - Call `call_senior_engineer(blackboard_state)` for each sprint task.
   - Pass only the specific task description and the current `blackboard_state`.
5. **Update Blackboard:** Merge the subagent's JSON output (modified files, test results) into the central Blackboard.
6. **Finalize:** When the plan is complete, summarize the final Blackboard state to the human user.
