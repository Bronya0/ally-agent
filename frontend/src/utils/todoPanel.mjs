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

// The panel is a task handoff view rather than a historical log: when work is
// active, surface the most recently completed item, the current item, and then
// pending work at the top. Remaining items stay available below the fold.
export function orderTodoPanelEntries(todos) {
  const entries = normalizeTodoEntries(todos);
  const current = entries.find((entry) => entry.status === 'in_progress');
  const pending = entries.filter((entry) => entry.status === 'pending');
  const done = entries.filter((entry) => entry.status === 'done');

  if (!current) {
    return [...pending, ...done, ...entries.filter((entry) => entry.status !== 'pending' && entry.status !== 'done')];
  }

  const completedBeforeCurrent = entries
    .filter((entry) => entry.sourceIndex < current.sourceIndex && entry.status === 'done');
  const previousDone = completedBeforeCurrent[completedBeforeCurrent.length - 1] || done[done.length - 1];
  const used = new Set();
  const ordered = [];
  const append = (entry) => {
    if (!entry || used.has(entry.sourceIndex)) return;
    used.add(entry.sourceIndex);
    ordered.push(entry);
  };

  append(previousDone);
  append(current);
  for (const entry of pending) append(entry);
  for (const entry of entries) append(entry);
  return ordered;
}
