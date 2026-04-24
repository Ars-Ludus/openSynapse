<script lang="ts">
  import { onMount } from 'svelte';
  import { GetFileCode } from '../../wailsjs/go/main/App';

  let { fileId }: { fileId: string } = $props();

  let lines = $state<string[]>([]);
  let loading = $state(true);
  let error = $state('');

  onMount(async () => {
    try {
      const code = await GetFileCode(fileId);
      lines = (code ?? '').split('\n');
      // Drop trailing empty line from final newline
      if (lines.length > 0 && lines[lines.length - 1] === '') {
        lines.pop();
      }
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  });

  let lineNumWidth = $derived(String(lines.length).length);
</script>

<div class="container">
  {#if loading}
    <div class="status">Loading…</div>
  {:else if error}
    <div class="status error">{error}</div>
  {:else if lines.length === 0}
    <div class="status">No code found.</div>
  {:else}
    <pre class="code-block"><code>{#each lines as line, i (i)}<div class="line"><span class="ln" style="min-width: {lineNumWidth + 1}ch">{i + 1}</span><span class="lc">{line}</span></div>{/each}</code></pre>
  {/if}
</div>

<style>
  .container {
    height: 100%;
    overflow: auto;
    background: var(--bg-editor);
  }

  .status {
    color: var(--text-muted);
    padding: 24px;
    text-align: center;
    font-family: var(--font-ui);
  }

  .status.error {
    color: #f48771;
  }

  .code-block {
    margin: 0;
    padding: 8px 0;
    font-family: var(--font-mono);
    font-size: 13px;
    line-height: 1.6;
    white-space: pre;
    counter-reset: line;
  }

  .line {
    display: flex;
    min-height: 1.6em;
  }

  .line:hover {
    background: rgba(255, 255, 255, 0.03);
  }

  .ln {
    display: inline-block;
    text-align: right;
    padding: 0 12px 0 16px;
    color: var(--line-number);
    user-select: none;
    flex-shrink: 0;
    border-right: 1px solid var(--border);
    margin-right: 12px;
  }

  .lc {
    white-space: pre;
    color: var(--text-primary);
  }
</style>
