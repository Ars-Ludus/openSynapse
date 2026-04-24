import type { main } from '../../wailsjs/go/models';

export type FileNode = main.FileNode;
export type SnippetView = main.SnippetView;
export type FileInfoView = main.FileInfoView;

export type TabType = 'snippets' | 'info' | 'code';

export interface OpenPanel {
  fileId: string;
  filePath: string;
  fileName: string;
  activeTab: TabType;
}

class AppState {
  fileTree = $state<FileNode[]>([]);
  openPanels = $state<OpenPanel[]>([]);
  /** wikilinkColors[snippetId][wikilink] = "#hexcolor" */
  wikilinkColors = $state<Record<string, Record<string, string>>>({});

  openFile(fileId: string, filePath: string): void {
    // Don't open duplicates
    if (this.openPanels.some((p) => p.fileId === fileId)) return;

    const fileName = filePath.split('/').pop() ?? filePath;
    const panel: OpenPanel = { fileId, filePath, fileName, activeTab: 'snippets' };

    if (this.openPanels.length >= 2) {
      // Replace rightmost panel
      this.openPanels[1] = panel;
    } else {
      this.openPanels.push(panel);
    }
  }

  closePanel(fileId: string): void {
    const idx = this.openPanels.findIndex((p) => p.fileId === fileId);
    if (idx !== -1) this.openPanels.splice(idx, 1);
  }

  setActiveTab(fileId: string, tab: TabType): void {
    const panel = this.openPanels.find((p) => p.fileId === fileId);
    if (panel) panel.activeTab = tab;
  }

  getWikilinkColor(snippetId: string, wikilink: string): string {
    return this.wikilinkColors[snippetId]?.[wikilink] ?? '#ffffff';
  }

  setWikilinkColor(snippetId: string, wikilink: string, color: string): void {
    if (!this.wikilinkColors[snippetId]) {
      this.wikilinkColors[snippetId] = {};
    }
    this.wikilinkColors[snippetId][wikilink] = color;
  }
}

export const appState = new AppState();
