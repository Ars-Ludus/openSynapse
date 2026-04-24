<script lang="ts">
  import { untrack } from 'svelte';
  import { SetWikilinkColor } from '../../wailsjs/go/main/App';

  let {
    snippetId,
    wikilink,
    currentColor,
    x,
    y,
    onApply,
    onClose,
  }: {
    snippetId: string;
    wikilink: string;
    currentColor: string;
    x: number;
    y: number;
    onApply: (color: string) => void;
    onClose: () => void;
  } = $props();

  // untrack() reads the prop outside of Svelte's reactive graph, so the state
  // captures the initial value without triggering the state_referenced_locally warning.
  let selectedColor = $state(untrack(() => currentColor));
  let applying = $state(false);
  let errorMsg = $state('');

  async function apply(): Promise<void> {
    applying = true;
    errorMsg = '';
    try {
      await SetWikilinkColor(snippetId, wikilink, selectedColor);
      onApply(selectedColor);
    } catch (e) {
      errorMsg = String(e);
    } finally {
      applying = false;
    }
  }

  function onOverlayClick(e: MouseEvent): void {
    if (e.target === e.currentTarget) onClose();
  }

  // Compute adjusted position to keep dialog within viewport
  const DIALOG_W = 220;
  const DIALOG_H = 180;
  let finalX = $derived(Math.min(x, window.innerWidth - DIALOG_W - 8));
  let finalY = $derived(Math.min(y, window.innerHeight - DIALOG_H - 8));
</script>

<div
  class="overlay"
  role="presentation"
  onclick={onOverlayClick}
  onkeydown={(e) => e.key === 'Escape' && onClose()}
>
  <div
    class="dialog"
    style="left: {finalX}px; top: {finalY}px;"
    role="dialog"
    aria-label="Pick wikilink color"
  >
    <div class="dialog-header">
      <span class="dialog-title">Color for <code>[[{wikilink}]]</code></span>
      <button class="close-btn" onclick={onClose}>×</button>
    </div>

    <div class="color-row">
      <input type="color" bind:value={selectedColor} class="color-input" />
      <span class="color-value">{selectedColor}</span>
    </div>

    <div class="presets">
      {#each ['#ffffff', '#4fc1ff', '#569cd6', '#4ec9b0', '#c586c0', '#ce9178', '#d7ba7d', '#b5cea8', '#f48771', '#6a9955'] as preset (preset)}
        <button
          class="preset"
          style="background: {preset}; {selectedColor === preset ? 'outline: 2px solid white; outline-offset: 1px;' : ''}"
          onclick={() => (selectedColor = preset)}
          title={preset}
        ></button>
      {/each}
    </div>

    {#if errorMsg}
      <div class="error">{errorMsg}</div>
    {/if}

    <div class="dialog-footer">
      <button class="btn-cancel" onclick={onClose}>Cancel</button>
      <button class="btn-apply" onclick={apply} disabled={applying}>
        {applying ? 'Applying…' : 'Apply'}
      </button>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 1000;
  }

  .dialog {
    position: fixed;
    background: var(--bg-modal);
    border: 1px solid var(--border-light);
    border-radius: 6px;
    padding: 12px;
    width: 220px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .dialog-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .dialog-title {
    font-size: 12px;
    color: var(--text-secondary);
  }

  .dialog-title code {
    font-family: var(--font-mono);
    color: var(--text-primary);
  }

  .close-btn {
    font-size: 16px;
    color: var(--text-muted);
    line-height: 1;
    padding: 0 2px;
  }

  .close-btn:hover {
    color: var(--text-primary);
  }

  .color-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .color-input {
    width: 40px;
    height: 32px;
    border: none;
    border-radius: 3px;
    padding: 2px;
    background: var(--bg-input);
    cursor: pointer;
  }

  .color-value {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--text-secondary);
  }

  .presets {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .preset {
    width: 18px;
    height: 18px;
    border-radius: 3px;
    border: 1px solid rgba(255, 255, 255, 0.15);
    cursor: pointer;
    transition: transform 0.1s;
  }

  .preset:hover {
    transform: scale(1.15);
  }

  .error {
    font-size: 11px;
    color: #f48771;
  }

  .dialog-footer {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
  }

  .btn-cancel,
  .btn-apply {
    padding: 4px 12px;
    border-radius: 3px;
    font-size: 12px;
  }

  .btn-cancel {
    background: var(--bg-input);
    color: var(--text-secondary);
  }

  .btn-cancel:hover {
    color: var(--text-primary);
  }

  .btn-apply {
    background: var(--accent);
    color: #fff;
  }

  .btn-apply:hover:not(:disabled) {
    background: var(--accent-hover);
  }

  .btn-apply:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
