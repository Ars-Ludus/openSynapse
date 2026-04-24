<script lang="ts">
  import { appState, type OpenPanel, type TabType } from '../lib/state.svelte';
  import SnippetAssemblyView from './SnippetAssemblyView.svelte';
  import FileInfoView from './FileInfoView.svelte';
  import CodeView from './CodeView.svelte';

  let { panel }: { panel: OpenPanel } = $props();

  const tabs: { id: TabType; label: string }[] = [
    { id: 'snippets', label: 'Snippet Assembly' },
    { id: 'info', label: 'File Info' },
    { id: 'code', label: 'Code' },
  ];
</script>

<div class="panel">
  <div class="tab-bar">
    <div class="tabs">
      {#each tabs as tab (tab.id)}
        <button
          class="tab"
          class:active={panel.activeTab === tab.id}
          onclick={() => appState.setActiveTab(panel.fileId, tab.id)}
        >
          {tab.label}
        </button>
      {/each}
    </div>
    <div class="panel-title" title={panel.filePath}>
      {panel.fileName}
    </div>
    <button class="close-btn" onclick={() => appState.closePanel(panel.fileId)} title="Close">×</button>
  </div>

  <div class="panel-content">
    {#if panel.activeTab === 'snippets'}
      <SnippetAssemblyView fileId={panel.fileId} />
    {:else if panel.activeTab === 'info'}
      <FileInfoView fileId={panel.fileId} />
    {:else}
      <CodeView fileId={panel.fileId} />
    {/if}
  </div>
</div>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }

  .tab-bar {
    display: flex;
    align-items: center;
    background: var(--bg-tab-bar);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
    min-height: 35px;
    overflow: hidden;
  }

  .tabs {
    display: flex;
    flex-shrink: 0;
  }

  .tab {
    padding: 6px 14px;
    font-size: 12px;
    color: var(--text-secondary);
    border-bottom: 2px solid transparent;
    white-space: nowrap;
    transition: color 0.1s;
  }

  .tab:hover {
    color: var(--text-primary);
    background: rgba(255, 255, 255, 0.04);
  }

  .tab.active {
    color: var(--text-primary);
    border-bottom-color: var(--tab-active-border);
    background: var(--bg-tab-active);
  }

  .panel-title {
    flex: 1;
    padding: 0 8px;
    font-size: 12px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    text-align: right;
  }

  .close-btn {
    padding: 4px 10px;
    font-size: 16px;
    color: var(--text-muted);
    flex-shrink: 0;
    line-height: 1;
  }

  .close-btn:hover {
    color: var(--text-primary);
    background: rgba(255, 255, 255, 0.08);
  }

  .panel-content {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }
</style>
