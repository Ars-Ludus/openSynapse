<script lang="ts">
  import { onMount } from 'svelte';
  import { GetFileInfo } from '../../wailsjs/go/main/App';
  import { type FileInfoView as FileInfoData } from '../lib/state.svelte';

  let { fileId }: { fileId: string } = $props();

  let info = $state<FileInfoData | null>(null);
  let loading = $state(true);
  let error = $state('');

  onMount(async () => {
    try {
      info = await GetFileInfo(fileId);
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  });

  function formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
  }

  function formatDate(iso: string): string {
    try {
      return new Date(iso).toLocaleString();
    } catch {
      return iso;
    }
  }
</script>

<div class="container">
  {#if loading}
    <div class="status">Loading…</div>
  {:else if error}
    <div class="status error">{error}</div>
  {:else if !info}
    <div class="status">File not found in index.</div>
  {:else}
    <table class="info-table">
      <tbody>
        <tr>
          <td class="label">Path</td>
          <td class="value mono">{info.path}</td>
        </tr>
        <tr>
          <td class="label">Language</td>
          <td class="value">
            <span class="lang-badge">{info.language}</span>
          </td>
        </tr>
        <tr>
          <td class="label">File Size</td>
          <td class="value">{formatBytes(info.file_size)}</td>
        </tr>
        <tr>
          <td class="label">Last Modified</td>
          <td class="value">{formatDate(info.last_modified)}</td>
        </tr>
        <tr>
          <td class="label">Summary</td>
          <td class="value summary">{info.file_summary || '—'}</td>
        </tr>
        <tr>
          <td class="label">Dependencies</td>
          <td class="value">
            {#if info.dependencies && info.dependencies.length > 0}
              <ul class="dep-list">
                {#each info.dependencies as dep (dep)}
                  <li class="mono">{dep}</li>
                {/each}
              </ul>
            {:else}
              <span class="muted">none</span>
            {/if}
          </td>
        </tr>
      </tbody>
    </table>
  {/if}
</div>

<style>
  .container {
    height: 100%;
    overflow-y: auto;
    padding: 16px;
  }

  .status {
    color: var(--text-muted);
    padding: 24px;
    text-align: center;
  }

  .status.error {
    color: #f48771;
  }

  .info-table {
    width: 100%;
    border-collapse: collapse;
  }

  .info-table tr {
    border-bottom: 1px solid var(--border);
  }

  .info-table td {
    padding: 10px 8px;
    vertical-align: top;
  }

  .label {
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    white-space: nowrap;
    padding-right: 16px;
    width: 120px;
  }

  .value {
    color: var(--text-primary);
    font-size: 13px;
    word-break: break-word;
  }

  .mono {
    font-family: var(--font-mono);
    font-size: 12px;
  }

  .summary {
    color: var(--text-secondary);
    line-height: 1.6;
  }

  .lang-badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 10px;
    background: var(--accent);
    color: #fff;
    font-size: 11px;
    font-weight: 600;
  }

  .dep-list {
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .dep-list li {
    color: var(--text-link);
    font-size: 12px;
  }

  .muted {
    color: var(--text-muted);
  }
</style>
