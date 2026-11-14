// Knowledge Base Type Definitions
// 知识库相关的 TypeScript 类型定义

export interface Notebook {
  id: string;
  userId: string;
  workspaceId?: string;
  name: string;
  parentId?: string;
  icon?: string;
  color?: string;
  sortOrder: number;
  createdAt: number;
  updatedAt: number;
  noteCount?: number;
  children?: Notebook[];
}

export interface NotebookInput {
  name: string;
  parentId?: string;
  icon?: string;
  color?: string;
  sortOrder?: number;
}

export interface NoteLink {
  id: string;
  sourceNoteId: string;
  targetNoteId: string;
  linkType: LinkType;
  confidence: number;
  createdBy: 'user' | 'ai';
  reason?: string;
  createdAt: number;
}

export type LinkType = 'related' | 'reference' | 'parent' | 'child' | 'bidirectional';

export interface LinkInput {
  targetNoteId: string;
  linkType: LinkType;
}

export interface RelatedNotesResponse {
  manualLinks: NoteLink[];
  smartLinks: NoteLink[];
  backlinks: NoteLink[];
  aiRecommendations?: NoteLink[];
}

export interface KnowledgeSearchResult {
  id: string;
  type: 'note' | 'memory' | 'email' | 'meeting';
  source: 'local' | 'memora' | 'email' | 'meeting';
  title: string;
  snippet: string;
  highlights?: string[];
  notebook?: {
    id: string;
    name: string;
  };
  tags?: string[];
  domain?: string;
  createdAt: string;
  updatedAt?: string;
  score: number;
  metadata?: Record<string, string>;
}

export interface KnowledgeSearchResponse {
  results: KnowledgeSearchResult[];
  total: number;
  limit: number;
  offset: number;
  searchTime: number; // milliseconds
}

export interface SearchParams {
  q: string; // 搜索关键词（必需）
  source?: 'all' | 'notes' | 'memora' | 'emails' | 'meetings';
  notebook?: string; // 笔记本ID过滤
  tags?: string; // 逗号分隔的标签列表
  domain?: 'work' | 'study' | 'life' | 'idea';
  dateFrom?: string; // ISO 8601
  dateTo?: string; // ISO 8601
  limit?: number; // 默认50，最大200
  offset?: number; // 默认0
}

export interface SearchFilters {
  source: 'all' | 'notes' | 'memora' | 'emails' | 'meetings';
  notebook?: string;
  tags: string[];
  domain?: 'work' | 'study' | 'life' | 'idea';
  dateRange?: {
    from?: Date;
    to?: Date;
  };
}

export interface Note {
  id: string;
  userId: string;
  workspaceId?: string;
  notebookId?: string;
  title?: string;
  content: string;
  contentType: string;
  tags?: string[];
  domain?: string;
  isLatest: boolean;
  createdAt: number;
  updatedAt: number;
  summary?: string;
  source?: string;
  isPinned?: boolean;
}

export interface Tag {
  name: string;
  count: number;
  color?: string;
}

export interface SyncState {
  noteId: string;
  memoraId?: string;
  contentHash: string;
  lastSyncedAt?: number;
  syncStatus: 'pending' | 'synced' | 'conflict' | 'error';
  errorMessage?: string;
}

// API Response types
export interface ApiResponse<T> {
  data?: T;
  error?: string;
  message?: string;
}

export interface NotebooksResponse {
  notebooks: Notebook[];
}

export interface NotebookNotesResponse {
  notes: Note[];
  total: number;
}

// UI State types
export interface NotebookTreeNode extends Notebook {
  expanded?: boolean;
  selected?: boolean;
  level?: number;
}

export interface SearchState {
  query: string;
  filters: SearchFilters;
  results: KnowledgeSearchResult[];
  total: number;
  loading: boolean;
  searchTime: number;
  error?: string;
}

export interface NotebookState {
  notebooks: Notebook[];
  currentNotebook?: Notebook;
  loading: boolean;
  error?: string;
}

export interface NoteLinkState {
  links: NoteLink[];
  relatedNotes?: RelatedNotesResponse;
  loading: boolean;
  error?: string;
}
