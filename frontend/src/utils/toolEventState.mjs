export function findToolEventMessage(session, eventId) {
  if (!session || !eventId || !Array.isArray(session.messages)) return null;
  const cached = session._lastToolEventId === eventId ? session._lastToolMsg : null;
  if (cached && cached.eventId === eventId && session.messages.includes(cached)) return cached;

  const found = session.messages.find(
    (item) => item?.role === 'tool_call' && item.eventId === eventId,
  ) || null;
  session._lastToolEventId = eventId;
  session._lastToolMsg = found;
  return found;
}

export function commitToolEventMessage(session, eventId, existing, payload) {
  if (!session || !eventId || !Array.isArray(session.messages)) return null;
  let target = existing;
  if (!target || target.eventId !== eventId || !session.messages.includes(target)) {
    target = findToolEventMessage(session, eventId);
  }

  if (target) {
    for (const key of Object.keys(payload)) {
      if (target[key] !== payload[key]) target[key] = payload[key];
    }
  } else {
    target = payload;
    session.messages.push(target);
  }

  session._lastToolEventId = eventId;
  session._lastToolMsg = target;
  return target;
}
