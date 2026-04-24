<script lang="ts">
  import DirectoryTree from './DirectoryTree.svelte';
  import { appState, type FileNode } from '../lib/state.svelte';

  let { nodes, depth = 0 }: { nodes: FileNode[]; depth?: number } = $props();

  function openFile(node: FileNode): void {
    if (node.file_id) {
      appState.openFile(node.file_id, node.path);
    }
  }
</script>

{#each nodes as node (node.name + node.path)}
  {#if node.is_dir}
    <details open class="dir-node">
      <summary class="dir-summary" style="padding-left: {depth * 12}px">
        <span class="caret">›</span>
        <span class="icon dir-icon">📁</span>
        <span class="dir-name">{node.name}</span>
      </summary>
      {#if node.children && node.children.length > 0}
        <DirectoryTree nodes={node.children} depth={depth + 1} />
      {/if}
    </details>
  {:else}
    <button
      class="file-node"
      style="padding-left: {depth * 12 + 20}px"
      onclick={() => openFile(node)}
      title={node.path}
    >
      <span class="icon file-icon">📄</span>
      <span class="file-name">{node.name}</span>
    </button>
  {/if}
{/each}

<style>
  .dir-node {
    display: block;
    user-select: none;
  }

  .dir-summary {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 2px 8px 2px 0;
    cursor: pointer;
    color: var(--text-primary);
    list-style: none;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .dir-summary::-webkit-details-marker {
    display: none;
  }

  .dir-summary:hover {
    background: var(--bg-sidebar-hover);
  }

  .caret {
    display: inline-block;
    width: 12px;
    font-size: 12px;
    transition: transform 0.1s;
    flex-shrink: 0;
  }

  details[open] > summary .caret {
    transform: rotate(90deg);
  }

  .file-node {
    display: flex;
    align-items: center;
    gap: 4px;
    width: 100%;
    padding-top: 2px;
    padding-bottom: 2px;
    padding-right: 8px;
    text-align: left;
    color: var(--text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .file-node:hover {
    background: var(--bg-sidebar-hover);
  }

  .icon {
    font-size: 13px;
    flex-shrink: 0;
  }

  .dir-name,
  .file-name {
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
