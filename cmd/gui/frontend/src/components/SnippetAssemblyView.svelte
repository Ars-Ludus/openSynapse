<script lang="ts">
  import { onMount } from 'svelte';
  import { GetSnippetAssembly } from '../../wailsjs/go/main/App';
  import { type SnippetView } from '../lib/state.svelte';
  import WikilinkChip from './WikilinkChip.svelte';

  let { fileId }: { fileId: string } = $props();

  let snippets = $state<SnippetView[]>([]);
  let loading = $state(true);
  let error = $state('');

  onMount(async () => {
    try {
      snippets = (await GetSnippetAssembly(fileId)) ?? [];
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  });

  function badgeColor(type: string): string {
    const map: Record<string, string> = {
      function: 'var(--badge-fn)',
      method: 'var(--badge-method)',
      class: 'var(--badge-class)',
      struct: 'var(--badge-struct)',
      interface: 'var(--badge-interface)',
      variable: 'var(--badge-var)',
      constant: 'var(--badge-const)',
    };
    return map[type] ?? 'var(--badge-unknown)';
  }
</script>

<div class="container">
  {#if loading}
    <div class="status">Loading snippets…</div>
  {:else if error}
    <div class="status error">{error}</div>
  {:else if snippets.length === 0}
    <div class="status">No snippets found for this file.</div>
  {:else}
    {#each snippets as snippet (snippet.snippet_id)}
      <div class="snippet-card">
        <div class="snippet-header">
          <span class="type-badge" style="color: {badgeColor(snippet.snippet_type)}">
            {snippet.snippet_type}
          </span>
          <span class="snippet-name">{snippet.name}</span>
          <span class="lines">lines {snippet.line_start}–{snippet.line_end}</span>
        </div>
        {#if snippet.description}
          <p class="description">{snippet.description}</p>
        {/if}
        {#if snippet.wikilinks && snippet.wikilinks.length > 0}
          <div class="wikilinks">
            {#each snippet.wikilinks as wikilink (wikilink)}
              <WikilinkChip snippetId={snippet.snippet_id} {wikilink} />
            {/each}
          </div>
        {/if}
      </div>
    {/each}
  {/if}
</div>

<style>
  .container {
    height: 100%;
    overflow-y: auto;
    padding: 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .status {
    color: var(--text-muted);
    padding: 24px;
    text-align: center;
  }

  .status.error {
    color: #f48771;
  }

  .snippet-card {
    background: var(--bg-snippet-card);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .snippet-header {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .type-badge {
    font-size: 11px;
    font-weight: 600;
    text-transform: lowercase;
    letter-spacing: 0.02em;
    font-family: var(--font-mono);
  }

  .snippet-name {
    font-family: var(--font-mono);
    font-size: 13px;
    font-weight: 600;
    color: var(--text-primary);
    flex: 1;
  }

  .lines {
    font-size: 11px;
    color: var(--text-muted);
    margin-left: auto;
    white-space: nowrap;
  }

  .description {
    font-size: 12px;
    color: var(--text-secondary);
    line-height: 1.5;
  }

  .wikilinks {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 2px;
  }
</style>
