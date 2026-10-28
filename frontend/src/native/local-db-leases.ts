import { SYNC_MODULES, isSyncMode, isSyncModule, type SyncMode, type SyncModule } from '../api/syncPolicyContract'

export interface OwnerIdentity { readonly userId: string; readonly workspaceId: string }
export interface OwnerLease { readonly epoch: object; readonly principal: OwnerIdentity | null }
export interface OperationOptions { readonly kind: 'local-edit' | 'cloud-sync'; readonly module?: SyncModule }
export interface OperationLease extends OperationOptions { readonly owner: OwnerLease }
export type ActivationPublisher = () => undefined
export type PolicySnapshot = Readonly<Record<SyncModule, SyncMode>>
export type PolicySnapshotPublisher = (snapshot: PolicySnapshot) => undefined

interface PolicyState { mode: SyncMode; epoch: object; pending?: object }
interface OwnerTicket { owner: OwnerLease }
interface PolicyTicket { ownerEpoch: object; module: SyncModule; mode: SyncMode; epoch: object }
interface SnapshotTicket {
  ownerEpoch: object
  snapshot: PolicySnapshot
  entries: { module: SyncModule; mode: SyncMode; epoch: object }[]
}
interface IssuedOperation { ownerEpoch: object; connectionEpoch: object; policyEpoch?: object }

function epoch(): object { return Object.freeze({}) }

export function expiredOperation(outcome?: 'committed'): Error {
  return Object.assign(new Error('LocalDB operation lease expired'), { name: 'AbortError', outcome })
}

/** Per-instance ordering only; this neither assigns historical rows nor locks other runtimes. */
export class LocalDbLeases {
  private ownerEpoch = epoch()
  private owner: OwnerLease = Object.freeze({ epoch: this.ownerEpoch, principal: null })
  private pendingOwner?: OwnerTicket
  private pendingSnapshot?: SnapshotTicket
  private connectionEpoch = epoch()
  private policies = new Map<SyncModule, PolicyState>()
  private issued = new WeakMap<OperationLease, IssuedOperation>()

  capture(options: OperationOptions): OperationLease {
    if (this.pendingOwner) throw expiredOperation()
    if (options.kind !== 'local-edit' && options.kind !== 'cloud-sync') throw new TypeError('invalid operation kind')
    let policyEpoch: object | undefined
    if (options.kind === 'cloud-sync') {
      if (!options.module) throw new TypeError('cloud-sync requires a module')
      const policy = this.policies.get(options.module)
      if (!policy || policy.pending || policy.mode !== 'cloud') throw expiredOperation()
      policyEpoch = policy.epoch
    }
    const lease = Object.freeze({ ...options, owner: this.owner })
    this.issued.set(lease, { ownerEpoch: this.ownerEpoch, connectionEpoch: this.connectionEpoch, policyEpoch })
    return lease
  }

  assertCurrent(lease: OperationLease, outcome?: 'committed'): void {
    const issued = this.issued.get(lease)
    const policy = lease.module ? this.policies.get(lease.module) : undefined
    if (!issued || this.pendingOwner || issued.ownerEpoch !== this.ownerEpoch ||
      issued.connectionEpoch !== this.connectionEpoch ||
      (lease.kind === 'cloud-sync' && (!policy || policy.pending || policy.mode !== 'cloud' || policy.epoch !== issued.policyEpoch))) {
      throw expiredOperation(outcome)
    }
  }

  prepareOwner(principal: OwnerIdentity | null): OwnerTicket {
    this.ownerEpoch = epoch() // Revoke synchronously, even for A→B→A or repeated A.
    const owner = Object.freeze({ epoch: this.ownerEpoch, principal: principal ? Object.freeze({ ...principal }) : null })
    const ticket = { owner }
    this.pendingOwner = ticket
    return ticket
  }

  activateOwner(ticket: OwnerTicket, publish: ActivationPublisher): void {
    if (this.pendingOwner !== ticket) throw expiredOperation()
    this.publish(publish)
    // A synchronous watcher may have queued a newer activation during publish.
    // Admission stays closed until that ticket runs; never clear its pending state.
    if (this.pendingOwner !== ticket || this.ownerEpoch !== ticket.owner.epoch) throw expiredOperation()
    this.owner = ticket.owner
    this.pendingOwner = undefined
    this.policies.clear() // A new owner starts with no cloud permissions.
    this.pendingSnapshot = undefined
  }

  preparePolicy(module: SyncModule, mode: SyncMode): PolicyTicket {
    // A partial request cannot steal one member of an atomic snapshot. A newer
    // complete snapshot may supersede it; otherwise wait for its activation.
    if (this.pendingOwner || this.pendingSnapshot) throw expiredOperation()
    if (!isSyncModule(module)) throw new TypeError('invalid sync module')
    if (mode !== 'local' && mode !== 'cloud') throw new TypeError('invalid sync mode')
    const nextEpoch = epoch()
    const policy = this.policies.get(module) ?? { mode: 'local', epoch: nextEpoch }
    const ticket = { ownerEpoch: this.ownerEpoch, module, mode, epoch: nextEpoch }
    policy.epoch = nextEpoch
    policy.pending = ticket
    this.policies.set(module, policy)
    return ticket
  }

  activatePolicy(ticket: PolicyTicket, publish: ActivationPublisher): void {
    const policy = this.policies.get(ticket.module)
    if (this.pendingOwner || ticket.ownerEpoch !== this.ownerEpoch || policy?.pending !== ticket) throw expiredOperation()
    this.publish(publish)
    if (this.pendingOwner || ticket.ownerEpoch !== this.ownerEpoch || policy.pending !== ticket) throw expiredOperation()
    policy.mode = ticket.mode
    policy.pending = undefined
  }

  prepareSnapshot(modes: PolicySnapshot): SnapshotTicket {
    if (!modes || typeof modes !== 'object' || Array.isArray(modes) ||
      Object.keys(modes).length !== SYNC_MODULES.length) throw new TypeError('complete policy snapshot required')
    const values = SYNC_MODULES.map(module => {
      const mode = modes[module]
      if (!Object.prototype.hasOwnProperty.call(modes, module) || !isSyncMode(mode)) {
        throw new TypeError('complete policy snapshot required')
      }
      return [module, mode] as const
    })
    // Validate and copy every value before revoking any admission.
    if (this.pendingOwner) throw expiredOperation()
    const snapshot = Object.freeze(Object.fromEntries(values)) as PolicySnapshot
    const entries = SYNC_MODULES.filter(module => {
      const current = this.policies.get(module)
      return !current || current.pending || current.mode !== snapshot[module]
    }).map(module => ({ module, mode: snapshot[module], epoch: epoch() }))
    const ticket = { ownerEpoch: this.ownerEpoch, snapshot, entries }
    for (const entry of entries) {
      const state = this.policies.get(entry.module) ?? { mode: 'local', epoch: entry.epoch }
      state.epoch = entry.epoch
      state.pending = ticket
      this.policies.set(entry.module, state)
    }
    this.pendingSnapshot = ticket
    return ticket
  }

  activateSnapshot(ticket: SnapshotTicket, publish: PolicySnapshotPublisher): void {
    const assertCurrent = () => {
      if (this.pendingOwner || ticket.ownerEpoch !== this.ownerEpoch || this.pendingSnapshot !== ticket ||
        ticket.entries.some(entry => {
          const state = this.policies.get(entry.module)
          return state?.pending !== ticket || state.epoch !== entry.epoch
        })) throw expiredOperation()
    }
    assertCurrent()
    this.publish(publish, ticket.snapshot)
    assertCurrent() // Synchronous publishers can request another snapshot/owner.
    for (const entry of ticket.entries) {
      const state = this.policies.get(entry.module)!
      state.mode = entry.mode
      state.pending = undefined
    }
    this.pendingSnapshot = undefined
  }

  invalidateConnection(): void { this.connectionEpoch = epoch() }

  private publish<T extends unknown[]>(publish: (...args: T) => undefined, ...args: T): void {
    // Never await a publisher: it must only atomically publish a prepared snapshot.
    if (Object.prototype.toString.call(publish) === '[object AsyncFunction]') throw new TypeError('activation publisher must be synchronous')
    if (publish(...args) !== undefined) throw new TypeError('activation publisher must return undefined')
  }
}
