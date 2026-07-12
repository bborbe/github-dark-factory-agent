Your final response MUST be a single JSON object with this schema:

```json
{
  "status": "done" | "failed" | "needs_input",
  "message": "human-readable summary of what happened",
  "files": ["list", "of", "files", "created-or-modified"]
}
```

Field rules:
- `status`: required — `done` (success), `failed` (error), or `needs_input` (blocked on missing info)
- `message`: required — concise summary, one or two sentences
- `files`: optional — list of file paths created or modified during execution

Do NOT wrap the JSON in markdown code fences. Output raw JSON only.
