- Be concise in comments, commit messages, and replies.

- Avoid magic numbers and strings. Extract recurring or meaningful
  values into named constants. Keep obvious one-off values inline.

- Keep identifiers unexported unless export is required. Ask before
  exporting existing private code.

- Do not touch code unrelated to the task. Minimize changed lines.

- Keep HTTP, service, repository, and driver concerns separate. Do not
  call lower layers directly from UI/HTTP.

- Commit small, cohesive changes. Avoid mixing refactors with behavior
  changes.

- Before committing, run relevant tests and formatting. If skipped, state
  why.

- Commit subjects: imperative, capitalized, no period, ≤50 chars. Use a
  body only for context/why, wrapped at 72 chars.

- For bug fixes, write the failing test first, then fix it, then verify
  the test passes.
