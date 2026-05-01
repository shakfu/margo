// Svelte action that re-typesets MathJax whenever the bound content changes.
// Debounced so streaming token-by-token updates don't trigger a typeset per chunk;
// the formula renders ~250ms after the stream stops emitting.
export function mathjax(node: HTMLElement, _content: string) {
  let timer: ReturnType<typeof setTimeout> | undefined;

  const typeset = () => {
    const mj = (window as any).MathJax;
    if (!mj || typeof mj.typesetPromise !== 'function') return;
    if (typeof mj.typesetClear === 'function') {
      try { mj.typesetClear([node]); } catch (_) {}
    }
    mj.typesetPromise([node]).catch(() => { /* swallow tex errors */ });
  };

  const schedule = () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(typeset, 250);
  };

  schedule();

  return {
    update(_newContent: string) {
      schedule();
    },
    destroy() {
      if (timer) clearTimeout(timer);
      const mj = (window as any).MathJax;
      if (mj && typeof mj.typesetClear === 'function') {
        try { mj.typesetClear([node]); } catch (_) {}
      }
    }
  };
}
