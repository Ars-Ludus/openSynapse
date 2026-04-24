<script lang="ts">
  import { onMount } from 'svelte';
  import { GetWikilinkColor, GetConnectedSnippetIDs, GetWikilinkEdges } from '../../wailsjs/go/main/App';
  import { appState } from '../lib/state.svelte';
  import ColorPickerDialog from './ColorPickerDialog.svelte';
  import type { main } from '../../wailsjs/go/models';

  let { snippetId, wikilink }: { snippetId: string; wikilink: string } = $props();

  let showPicker = $state(false);
  let pickerX = $state(0);
  let pickerY = $state(0);

  // Context menu state
  let showMenu = $state(false);
  let menuX = $state(0);
  let menuY = $state(0);

  // Hover tooltip state
  let showTooltip = $state(false);
  let tooltipX = $state(0);
  let tooltipY = $state(0);
  let edges = $state<main.WikilinkEdgeInfo[]>([]);
  let edgesLoaded = $state(false);
  let hoverTimer: ReturnType<typeof setTimeout> | null = null;

  // Derive color from global state so it updates when SetWikilinkColor propagates
  let color = $derived(appState.getWikilinkColor(snippetId, wikilink));

  onMount(async () => {
    try {
      const c = await GetWikilinkColor(snippetId, wikilink);
      appState.setWikilinkColor(snippetId, wikilink, c || '#ffffff');
    } catch {
      // leave default
    }
  });

  async function loadEdges(): Promise<void> {
    if (!edgesLoaded) {
      try {
        const result = await GetWikilinkEdges(snippetId, wikilink);
        edges = result ?? [];
      } catch {
        edges = [];
      }
      edgesLoaded = true;
    }
  }

  function onContextMenu(e: MouseEvent): void {
    e.preventDefault();
    menuX = e.clientX;
    menuY = e.clientY;
    showMenu = true;
    loadEdges();
  }

  async function onMouseEnter(e: MouseEvent): Promise<void> {
    tooltipX = e.clientX + 12;
    tooltipY = e.clientY + 12;
    hoverTimer = setTimeout(async () => {
      showTooltip = true;
      await loadEdges();
    }, 300);
  }

  function onMouseMove(e: MouseEvent): void {
    tooltipX = e.clientX + 12;
    tooltipY = e.clientY + 12;
  }

  function onMouseLeave(): void {
    if (hoverTimer) {
      clearTimeout(hoverTimer);
      hoverTimer = null;
    }
    showTooltip = false;
  }

  async function onColorApplied(newColor: string): Promise<void> {
    showPicker = false;
    appState.setWikilinkColor(snippetId, wikilink, newColor);
    try {
      const connectedIds = await GetConnectedSnippetIDs(snippetId);
      for (const id of connectedIds ?? []) {
        appState.setWikilinkColor(id, wikilink, newColor);
      }
    } catch {
      // non-fatal
    }
  }

  function openEdge(edge: main.WikilinkEdgeInfo): void {
    showMenu = false;
    appState.openFile(edge.file_id, edge.file_path);
  }

  function openColorPicker(): void {
    showMenu = false;
    pickerX = menuX;
    pickerY = menuY;
    showPicker = true;
  }

  function textColor(bg: string): string {
    const hex = bg.replace('#', '');
    if (hex.length !== 6) return '#1e1e1e';
    const r = parseInt(hex.slice(0, 2), 16);
    const g = parseInt(hex.slice(2, 4), 16);
    const b = parseInt(hex.slice(4, 6), 16);
    const luminance = (0.299 * r + 0.587 * g + 0.114 * b) / 255;
    return luminance > 0.5 ? '#1e1e1e' : '#ffffff';
  }

  /** Strip common path prefix for display — show last 2-3 segments only */
  function shortPath(path: string): string {
    const parts = path.split('/');
    return parts.slice(-3).join('/');
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<span
  class="chip"
  style="background-color: {color}; color: {textColor(color)};"
  oncontextmenu={onContextMenu}
  onmouseenter={onMouseEnter}
  onmousemove={onMouseMove}
  onmouseleave={onMouseLeave}
>
  [[{wikilink}]]
</span>

{#if showMenu}
  <div class="menu-backdrop" onclick={() => (showMenu = false)} role="presentation"></div>
  <div class="context-menu" style="left: {menuX}px; top: {menuY}px;">
    {#if !edgesLoaded}
      <div class="menu-row menu-loading">Loading…</div>
    {:else if edges.length > 0}
      {#each edges as edge (edge.snippet_id)}
        <button class="menu-row menu-item" onclick={() => openEdge(edge)}>
          <span class="menu-item-path">{shortPath(edge.file_path)}</span>
          <span class="menu-item-name">{edge.name}</span>
        </button>
      {/each}
      <div class="menu-divider"></div>
    {/if}
    <button class="menu-row menu-item" onclick={openColorPicker}>
      Change color…
    </button>
  </div>
{/if}

{#if showPicker}
  <ColorPickerDialog
    {snippetId}
    {wikilink}
    currentColor={color}
    x={pickerX}
    y={pickerY}
    onApply={onColorApplied}
    onClose={() => (showPicker = false)}
  />
{/if}

{#if showTooltip}
  <div
    class="tooltip"
    style="left: {tooltipX}px; top: {tooltipY}px;"
  >
    <div class="tooltip-header">[[{wikilink}]] connections</div>
    {#if !edgesLoaded}
      <div class="tooltip-loading">Loading…</div>
    {:else if edges.length === 0}
      <div class="tooltip-empty">No edge-connected snippets found</div>
    {:else}
      <ul class="tooltip-list">
        {#each edges as edge (edge.snippet_id)}
          <li class="tooltip-item">
            <span class="edge-name">{edge.name}</span>
            <span class="edge-type">{edge.snippet_type}</span>
            <span class="edge-location">
              {shortPath(edge.file_path)}:{edge.line_start}
            </span>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
{/if}

<style>
  .chip {
    display: inline-block;
    padding: 2px 6px;
    border-radius: 3px;
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 500;
    cursor: context-menu;
    user-select: none;
    transition: filter 0.1s;
    white-space: nowrap;
  }

  .chip:hover {
    filter: brightness(1.15);
  }

  /* Tooltip — rendered in a fixed position over everything */
  .tooltip {
    position: fixed;
    z-index: 9999;
    background: #2d2d2d;
    border: 1px solid #454545;
    border-radius: 4px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
    min-width: 220px;
    max-width: 420px;
    pointer-events: none;
    font-size: 12px;
    color: var(--text-primary, #d4d4d4);
  }

  .tooltip-header {
    padding: 6px 10px;
    font-weight: 600;
    font-family: var(--font-mono);
    border-bottom: 1px solid #454545;
    color: #9cdcfe;
    white-space: nowrap;
  }

  .tooltip-loading,
  .tooltip-empty {
    padding: 8px 10px;
    color: #888;
    font-style: italic;
  }

  .tooltip-list {
    list-style: none;
    margin: 0;
    padding: 4px 0;
    max-height: 260px;
    overflow-y: auto;
  }

  .tooltip-item {
    display: flex;
    flex-direction: column;
    gap: 1px;
    padding: 5px 10px;
    border-bottom: 1px solid #3a3a3a;
  }

  .tooltip-item:last-child {
    border-bottom: none;
  }

  .edge-name {
    font-family: var(--font-mono);
    font-weight: 600;
    color: #dcdcaa;
  }

  .edge-type {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: #569cd6;
  }

  .edge-location {
    font-family: var(--font-mono);
    font-size: 10px;
    color: #888;
  }

  .menu-backdrop {
    position: fixed;
    inset: 0;
    z-index: 9998;
  }

  .context-menu {
    position: fixed;
    z-index: 9999;
    background: #2d2d2d;
    border: 1px solid #454545;
    border-radius: 4px;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5);
    min-width: 180px;
    max-width: 360px;
    font-size: 12px;
    color: var(--text-primary, #d4d4d4);
    padding: 3px 0;
  }

  .menu-row {
    display: flex;
    flex-direction: column;
    gap: 1px;
    padding: 5px 12px;
  }

  .menu-loading {
    color: #888;
    font-style: italic;
  }

  .menu-divider {
    height: 1px;
    background: #454545;
    margin: 3px 0;
    padding: 0;
  }

  .menu-item {
    width: 100%;
    background: none;
    border: none;
    color: inherit;
    text-align: left;
    cursor: pointer;
    border-radius: 3px;
  }

  .menu-item:hover {
    background: #3e3e3e;
  }

  .menu-item-path {
    font-family: var(--font-mono);
    font-size: 11px;
    color: #9cdcfe;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .menu-item-name {
    font-family: var(--font-mono);
    font-size: 10px;
    color: #888;
  }
</style>
