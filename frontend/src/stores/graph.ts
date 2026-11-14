import { defineStore } from 'pinia';
import { ref } from 'vue';

interface GraphNode {
  id: string;
  label: string;
  type: 'note' | 'notebook' | 'tag';
  size: number;
}

interface GraphEdge {
  source: string;
  target: string;
  type: 'link' | 'reference' | 'tag';
  weight: number;
}

interface GraphData {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

export const useGraphStore = defineStore('graph', () => {
  // State
  const graphData = ref<GraphData>({ nodes: [], edges: [] });
  const selectedNode = ref<string | null>(null);
  const loading = ref(false);
  const error = ref<string | null>(null);

  // Actions
  async function fetchGraph(noteId?: string) {
    loading.value = true;
    error.value = null;

    try {
      const url = noteId
        ? `/api/knowledge/graph/note/${noteId}`
        : '/api/knowledge/graph';

      const response = await fetch(url, {
        headers: {
          'Authorization': `Bearer ${localStorage.getItem('token')}`,
        },
      });

      if (!response.ok) {
        throw new Error('Failed to fetch graph data');
      }

      const data = await response.json();
      graphData.value = data;
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error';
      graphData.value = { nodes: [], edges: [] };
    } finally {
      loading.value = false;
    }
  }

  function selectNode(nodeId: string) {
    selectedNode.value = nodeId;
  }

  function clearGraph() {
    graphData.value = { nodes: [], edges: [] };
    selectedNode.value = null;
    error.value = null;
  }

  return {
    graphData,
    selectedNode,
    loading,
    error,
    fetchGraph,
    selectNode,
    clearGraph,
  };
});
