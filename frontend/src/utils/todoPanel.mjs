function normalizeTodoEntries(todos) {
  if (!Array.isArray(todos)) return [];
  return todos
    .filter((todo) => todo && typeof todo === 'object' && String(todo.title || '').trim())
    .map((todo, sourceIndex) => {
      const title = String(todo.title || '').trim();
      const status = String(todo.status || '').trim();
      return {
        key: `${sourceIndex}:${status}:${title}`,
        sourceIndex,
        status,
        title,
      };
    });
}

// The panel reads from the most recently completed item down: completed work
// (newest first), then the current item, then pending work, then anything
// else. Completed items stay visible instead of sinking to the bottom.
export function orderTodoPanelEntries(todos) {
  const entries = normalizeTodoEntries(todos);
  const done = entries.filter((entry) => entry.status === 'done').reverse();
  const current = entries.find((entry) => entry.status === 'in_progress');
  const pending = entries.filter((entry) => entry.status === 'pending');
  const rest = entries.filter(
    (entry) => entry.status !== 'done' && entry.status !== 'in_progress' && entry.status !== 'pending',
  );

  return [...done, ...(current ? [current] : []), ...pending, ...rest];
}
