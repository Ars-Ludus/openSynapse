<script lang="ts">
  import { appState } from '../lib/state.svelte';
  import FilePanel from './FilePanel.svelte';
</script>

<div class="split-layout">
  {#if appState.openPanels.length === 0}
    <div class="empty-state">
      <div class="empty-state-inner">
        <div class="empty-logo">⬡</div>
        <p>Select a file from the sidebar to open it</p>
      </div>
    </div>
  {:else}
    {#each appState.openPanels as panel, i (panel.fileId)}
      {#if i > 0}
        <div class="divider"></div>
      {/if}
      <div class="panel-wrapper">
        <FilePanel {panel} />
      </div>
    {/each}
  {/if}
</div>

<style>
  .split-layout {
    display: flex;
    flex-direction: row;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .panel-wrapper {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .divider {
    width: 1px;
    background: var(--border);
    flex-shrink: 0;
  }

  .empty-state {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--text-muted);
  }

  .empty-state-inner {
    text-align: center;
  }

  .empty-logo {
    font-size: 48px;
    margin-bottom: 16px;
    opacity: 0.3;
  }
</style>
