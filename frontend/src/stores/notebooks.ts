/**
 * notebooks.ts — Pinia store for knowledge base notebooks organization.
 *
 * Manages notebook hierarchy (tree structure) for organizing notes.
 * Supports nested notebooks with parent-child relationships.
 *
 * Reference: docs/2026-11-14-knowledge-base-integration-plan.md §5.2 & §9
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

export interface Notebook {
  id: string
  userId: string
  workspaceId?: string
  name: string
  parentId?: string
  icon?: string
  color?: string
  sortOrder: number
  createdAt: number
  updatedAt: number
  noteCount?: number
  children?: Notebook[]
}

interface NotebookInput {
  name: string
  parentId?: string
  icon?: string
  color?: string
}

export const useNotebooksStore = defineStore('notebooks', () => {
  // State
  const notebooks = ref<Notebook[]>([])
  const currentNotebook = ref<Notebook | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  // Getters
  const notebookById = computed(() => {
    const map = new Map<string, Notebook>()
    notebooks.value.forEach((nb) => map.set(nb.id, nb))
    return (id: string) => map.get(id) || null
  })

  const rootNotebooks = computed(() => {
    return notebooks.value.filter((nb) => !nb.parentId).sort((a, b) => a.sortOrder - b.sortOrder)
  })

  const notebookTree = computed(() => {
    const map = new Map<string, Notebook>()
    const roots: Notebook[] = []

    // First pass: create a map of all notebooks
    notebooks.value.forEach((nb) => {
      map.set(nb.id, { ...nb, children: [] })
    })

    // Second pass: build the tree structure
    map.forEach((nb) => {
      if (nb.parentId) {
        const parent = map.get(nb.parentId)
        if (parent) {
          parent.children = parent.children || []
          parent.children.push(nb)
        } else {
          // Parent not found, treat as root
          roots.push(nb)
        }
      } else {
        roots.push(nb)
      }
    })

    // Sort children by sortOrder
    const sortChildren = (notebook: Notebook) => {
      if (notebook.children && notebook.children.length > 0) {
        notebook.children.sort((a, b) => a.sortOrder - b.sortOrder)
        notebook.children.forEach(sortChildren)
      }
    }

    roots.forEach(sortChildren)
    return roots.sort((a, b) => a.sortOrder - b.sortOrder)
  })

  // Actions
  async function fetchNotebooks() {
    loading.value = true
    error.value = null
    try {
      const response = await fetch('/api/notebooks', {
        method: 'GET',
        headers: {
          'Content-Type': 'application/json',
        },
      })

      if (!response.ok) {
        throw new Error(`Failed to fetch notebooks: ${response.statusText}`)
      }

      const data = await response.json()
      notebooks.value = Array.isArray(data) ? data : data.notebooks || []
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error occurred'
      console.error('Error fetching notebooks:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function createNotebook(input: NotebookInput): Promise<Notebook> {
    loading.value = true
    error.value = null
    try {
      const response = await fetch('/api/notebooks', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(input),
      })

      if (!response.ok) {
        throw new Error(`Failed to create notebook: ${response.statusText}`)
      }

      const notebook: Notebook = await response.json()
      notebooks.value.push(notebook)
      return notebook
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error occurred'
      console.error('Error creating notebook:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function updateNotebook(id: string, updates: Partial<NotebookInput>): Promise<Notebook> {
    loading.value = true
    error.value = null
    try {
      const response = await fetch(`/api/notebooks/${id}`, {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(updates),
      })

      if (!response.ok) {
        throw new Error(`Failed to update notebook: ${response.statusText}`)
      }

      const updated: Notebook = await response.json()
      const index = notebooks.value.findIndex((nb) => nb.id === id)
      if (index !== -1) {
        notebooks.value[index] = updated
      }

      if (currentNotebook.value && currentNotebook.value.id === id) {
        currentNotebook.value = updated
      }

      return updated
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error occurred'
      console.error('Error updating notebook:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function deleteNotebook(id: string): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const response = await fetch(`/api/notebooks/${id}`, {
        method: 'DELETE',
        headers: {
          'Content-Type': 'application/json',
        },
      })

      if (!response.ok) {
        throw new Error(`Failed to delete notebook: ${response.statusText}`)
      }

      notebooks.value = notebooks.value.filter((nb) => nb.id !== id)
      if (currentNotebook.value && currentNotebook.value.id === id) {
        currentNotebook.value = null
      }
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error occurred'
      console.error('Error deleting notebook:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  function selectNotebook(notebook: Notebook | null) {
    currentNotebook.value = notebook
  }

  return {
    // State
    notebooks,
    currentNotebook,
    loading,
    error,

    // Getters
    notebookById,
    rootNotebooks,
    notebookTree,

    // Actions
    fetchNotebooks,
    createNotebook,
    updateNotebook,
    deleteNotebook,
    selectNotebook,
  }
})
