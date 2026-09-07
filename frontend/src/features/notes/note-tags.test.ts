/**
 * 本地规则提标签：不依赖网络也能一键打标。
 * Run: node --test --experimental-strip-types src/features/notes/note-tags.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { extractLocalTags, inferDomain, suggestTitle } from './note-tags.ts'

describe('extractLocalTags', () => {
  it('extracts canonical tech tags and skips empties', () => {
    const tags = extractLocalTags('用 Vue 和 Docker 部署 Kubernetes 集群')
    assert.deepEqual(tags, ['Vue', 'Docker', 'Kubernetes'])
  })

  it('returns empty for blank content', () => {
    assert.deepEqual(extractLocalTags('   '), [])
  })
})

describe('inferDomain / suggestTitle', () => {
  it('maps meeting keywords to work and idea keywords to idea', () => {
    assert.equal(inferDomain('下午周会讨论议程'), 'work')
    assert.equal(inferDomain('突然想到一个主意'), 'idea')
    assert.equal(inferDomain('学习 Kubernetes 原理'), 'study')
  })

  it('uses first sentence as title and caps at 24 chars', () => {
    assert.equal(suggestTitle('记得买菜。晚上做饭。'), '记得买菜')
    assert.equal(suggestTitle('甲'.repeat(40)).length, 24)
  })
})
