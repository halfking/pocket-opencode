/**
 * noteLinks.ts — Pinia store for note relationships and smart links.
 *
 * Manages connections between notes, including:
 * - Manual links (user-created)
 * - Smart links (AI-generated with confidence scores)
 * - Bidirectional links (backlinks)
 *
 * Reference: docs/2026-11-14-knowledge-base-integration-plan.md §5.2 & §9
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

export type LinkType = 'related' | 'reference' | 'parent' | 'child' | 'bidirectional'
export type LinkCreatedBy = 'user' | 'ai'

export interface NoteLink {
  id: string
  sourceNoteId: string
  targetNoteId: string
  linkType: LinkType
  confidence: number
  createdBy: LinkCreatedBy
  createdAt: number
  targetNote?: RelatedNote
  reason?: string
}

export interface RelatedNote {
  id: string
  title: string
  snippet: string
  tags: string[]
  createdAt: number
  updatedAt: number
}

export interface RelatedNotesResponse {
  manualLinks: NoteLink[]
  smartLinks: NoteLink[]
  backlinks: RelatedNote[]
}

interface CreateLinkInput {
  targetNoteId: string
  linkType: LinkType
}

export const useNoteLinksStore = defineStore('noteLinks', () => {
  // State
  const links = ref<NoteLink[]>([])
  const relatedNotes = ref<RelatedNotesResponse | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  // Getters
  const outgoingLinks = computed(() => {
    return (noteId: string) => {
      return links.value.filter((link) => link.sourceNoteId === noteId)
    }
  })

  const incomingLinks = computed(() => {
    return (noteId: string) => {
      return links.value.filter((link) => link.targetNoteId === noteId)
    }
  })

  // Actions
  async function fetchRelatedNotes(noteId: string): Promise<RelatedNotesResponse> {
    loading.value = true
    error.value = null
    try {
      const response = await fetch(`/api/notes/${noteId}/related`, {
        method: 'GET',
        headers: {
          'Content-Type': 'application/json',
        },
      })

      if (!response.ok) {
        throw new Error(`Failed to fetch related notes: ${response.statusText}`)
      }

      const data: RelatedNotesResponse = await response.json()
      relatedNotes.value = data

      // Update links cache with manual and smart links
      const allLinks = [...data.manualLinks, ...data.smartLinks]
      allLinks.forEach((link) => {
        const existingIndex = links.value.findIndex((l) => l.id === link.id)
        if (existingIndex !== -1) {
          links.value[existingIndex] = link
        } else {
          links.value.push(link)
        }
      })

      return data
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error occurred'
      console.error('Error fetching related notes:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function createLink(sourceNoteId: string, input: CreateLinkInput): Promise<NoteLink> {
    loading.value = true
    error.value = null
    try {
      const response = await fetch(`/api/notes/${sourceNoteId}/links`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(input),
      })

      if (!response.ok) {
        throw new Error(`Failed to create link: ${response.statusText}`)
      }

      const link: NoteLink = await response.json()
      links.value.push(link)

      // Refresh related notes if we're currently viewing them
      if (relatedNotes.value) {
        await fetchRelatedNotes(sourceNoteId)
      }

      return link
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error occurred'
      console.error('Error creating link:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function deleteLink(linkId: string): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const response = await fetch(`/api/note-links/${linkId}`, {
        method: 'DELETE',
        headers: {
          'Content-Type': 'application/json',
        },
      })

      if (!response.ok) {
        throw new Error(`Failed to delete link: ${response.statusText}`)
      }

      links.value = links.value.filter((link) => link.id !== linkId)

      // Update relatedNotes if currently loaded
      if (relatedNotes.value) {
        relatedNotes.value.manualLinks = relatedNotes.value.manualLinks.filter(
          (link) => link.id !== linkId
        )
        relatedNotes.value.smartLinks = relatedNotes.value.smartLinks.filter(
          (link) => link.id !== linkId
        )
      }
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error occurred'
      console.error('Error deleting link:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  function clearRelatedNotes() {
    relatedNotes.value = null
  }

  return {
    // State
    links,
    relatedNotes,
    loading,
    error,

    // Getters
    outgoingLinks,
    incomingLinks,

    // Actions
    fetchRelatedNotes,
    createLink,
    deleteLink,
    clearRelatedNotes,
  }
})
