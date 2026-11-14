import { defineStore } from 'pinia';
import { ref } from 'vue';

interface RecommendedNote {
  id: string;
  title: string;
  snippet: string;
  score: number;
  tags?: string[];
}

interface SuggestedTag {
  tag: string;
  confidence: number;
}

export const useRecommendationsStore = defineStore('recommendations', () => {
  // State
  const recommendations = ref<RecommendedNote[]>([]);
  const suggestedTags = ref<SuggestedTag[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);

  // Actions
  async function fetchRecommendations(noteId: string) {
    loading.value = true;
    error.value = null;

    try {
      const response = await fetch(`/api/notes/${noteId}/recommendations`, {
        headers: {
          'Authorization': `Bearer ${localStorage.getItem('token')}`,
        },
      });

      if (!response.ok) {
        throw new Error('Failed to fetch recommendations');
      }

      const data = await response.json();
      recommendations.value = data.recommendations || [];
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error';
      recommendations.value = [];
    } finally {
      loading.value = false;
    }
  }

  async function suggestTags(content: string) {
    loading.value = true;
    error.value = null;

    try {
      const response = await fetch('/api/notes/suggest-tags', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${localStorage.getItem('token')}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ content }),
      });

      if (!response.ok) {
        throw new Error('Failed to suggest tags');
      }

      const data = await response.json();
      suggestedTags.value = data.tags || [];
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error';
      suggestedTags.value = [];
    } finally {
      loading.value = false;
    }
  }

  function clearRecommendations() {
    recommendations.value = [];
    suggestedTags.value = [];
    error.value = null;
  }

  return {
    recommendations,
    suggestedTags,
    loading,
    error,
    fetchRecommendations,
    suggestTags,
    clearRecommendations,
  };
});
