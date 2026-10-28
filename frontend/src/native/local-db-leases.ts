import type { SyncMode, SyncModule } from '../api/syncPolicyContract'

export interface OwnerIdentity { readonly userId: string; readonly workspaceId: string }
export interface OwnerLease { readonly epoch: object; readonly principal: OwnerIdentity | null }
export interface OperationOptions { readonly kind: 'local-edit' | 'cloud-sync'; readonly module?: SyncModule }
export interface OperationLease extends OperationOptions { readonly owner: OwnerLease }
export type ActivationPublisher = () => undefined

interface PolicyState { mode: SyncMode; epoch: object; pending?: object }
interface OwnerTicket { owner: OwnerLease }
interface PolicyTicket { ownerEpoch: object; module: SyncModule; mode: SyncMode; epoch: object }
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
  }

  preparePolicy(module: SyncModule, mode: SyncMode): PolicyTicket {
    if (this.pendingOwner) throw expiredOperation()
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

  invalidateConnection(): void { this.connectionEpoch = epoch() }

  private publish(publish: ActivationPublisher): void {
    // Never await a publisher: it must only atomically publish a prepared snapshot.
    if (Object.prototype.toString.call(publish) === '[object AsyncFunction]') throw new TypeError('activation publisher must be synchronous')
    if (publish() !== undefined) throw new TypeError('activation publisher must return undefined')
  }
}
