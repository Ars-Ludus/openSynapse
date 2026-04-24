<script lang="ts">
  import { onMount } from 'svelte';
  import { GetFiles } from '../wailsjs/go/main/App';
  import { appState } from './lib/state.svelte';
  import DirectoryTree from './components/DirectoryTree.svelte';
  import SplitLayout from './components/SplitLayout.svelte';

  onMount(async () => {
    try {
      const files = await GetFiles();
      appState.fileTree = files ?? [];
    } catch (e) {
      console.error('GetFiles failed:', e);
    }
  });
</script>

<div class="layout">
  <aside class="sidebar">
    <div class="sidebar-header">
      <span class="sidebar-title">EXPLORER</span>
    </div>
    <div class="sidebar-tree">
      <DirectoryTree nodes={appState.fileTree} depth={0} />
    </div>
  </aside>
  <main class="editor">
    <SplitLayout />
  </main>
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: 260px 1fr;
    height: 100vh;
    overflow: hidden;
  }

  .sidebar {
    background: var(--bg-sidebar);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .sidebar-header {
    padding: 8px 12px 4px;
    flex-shrink: 0;
  }

  .sidebar-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.08em;
    color: var(--text-secondary);
    text-transform: uppercase;
  }

  .sidebar-tree {
    flex: 1;
    overflow-y: auto;
    overflow-x: hidden;
    padding-bottom: 8px;
  }

  .editor {
    display: flex;
    overflow: hidden;
    background: var(--bg-editor);
  }
</style>
