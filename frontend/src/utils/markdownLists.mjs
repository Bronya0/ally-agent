// SPDX-License-Identifier: GPL-3.0-only

// Native outside markers must fit inside the list box: message paint containment
// clips any overhang. Size each ordered list from its actual largest ordinal,
// instead of indenting every list for hypothetical three-digit numbers.
export function orderedListMarkerPlugin(md) {
  md.core.ruler.push('ordered_list_marker_width', (state) => {
    const lists = [];
    for (const token of state.tokens) {
      if (token.type === 'ordered_list_open' || token.type === 'bullet_list_open') {
        lists.push({ token, count: 0 });
      } else if (token.type === 'list_item_open') {
        if (lists.length) lists.at(-1).count++;
      } else if (token.type === 'ordered_list_close' || token.type === 'bullet_list_close') {
        const list = lists.pop();
        if (list?.token.type !== 'ordered_list_open') continue;
        const start = Number(list.token.attrGet('start') || 1);
        const digits = String(Math.max(start, start + list.count - 1)).length;
        const style = list.token.attrGet('style');
        list.token.attrSet('style', `${style ? style + ';' : ''}--md-list-marker-digits:${digits}`);
      }
    }
  });
}
