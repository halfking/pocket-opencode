import Capacitor
import Darwin
import Foundation
import SQLCipher

final class PocketBridgeViewController: CAPBridgeViewController {
    override func capacitorDidLoad() {
        super.capacitorDidLoad()
        bridge?.registerPluginInstance(DeviceBackupPlugin())
    }
}

@objc(DeviceBackupPlugin)
final class DeviceBackupPlugin: CAPPlugin, CAPBridgedPlugin {
    let identifier = "DeviceBackupPlugin"
    let jsName = "DeviceBackup"
    let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "snapshotDatabase", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "validateDatabase", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "ensureEmptyDatabase", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "publishDatabase", returnType: CAPPluginReturnPromise)
    ]

    private let queue = DispatchQueue(label: "pocket.device-backup")
    private let files = FileManager.default
    private let databaseName = "lobsterSQLite.db"
    private let sidecars = ["-wal", "-shm", "-journal"]
    private enum BackupFailure: Error { case invalidStage, unfinishedDatabase, invalidDatabase, occupiedTarget }

    @objc func snapshotDatabase(_ call: CAPPluginCall) {
        run(call, code: "BACKUP_SNAPSHOT", message: "Unable to copy the checkpointed local database") {
            let source = try self.database()
            try self.requireCheckpointed(source)
            let target = try self.candidate(call)
            try self.files.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
            try self.files.copyItem(at: source, to: target)
            do {
                try self.requireCheckpointed(source)
                guard try self.size(source) == self.size(target) else { throw BackupFailure.unfinishedDatabase }
            } catch {
                try? self.files.removeItem(at: target)
                throw error
            }
        }
    }

    @objc func validateDatabase(_ call: CAPPluginCall) {
        run(call, code: "BACKUP_DATABASE", message: "The backup database cannot be verified") {
            try self.validate(self.candidate(call), password: call.getString("password"))
        }
    }

    @objc func ensureEmptyDatabase(_ call: CAPPluginCall) {
        run(call, code: "BACKUP_TARGET", message: "The local database slot is not empty") {
            try self.requireEmpty(self.database())
        }
    }

    @objc func publishDatabase(_ call: CAPPluginCall) {
        run(call, code: "BACKUP_PUBLISH", message: "Unable to publish the verified backup database") {
            let target = try self.database()
            try self.requireEmpty(target)
            let source = try self.candidate(call)
            try self.validate(source, password: call.getString("password"))
            let handle = try FileHandle(forUpdating: source)
            defer { try? handle.close() }
            try handle.synchronize()
            try self.requireEmpty(target)
            let result = source.path.withCString { from in
                target.path.withCString { to in renamex_np(from, to, UInt32(RENAME_EXCL)) }
            }
            if result != 0 {
                if errno == EEXIST { throw BackupFailure.occupiedTarget }
                throw BackupFailure.unfinishedDatabase
            }
        }
    }

    private func run(_ call: CAPPluginCall, code: String, message: String, operation: @escaping () throws -> Void) {
        queue.async {
            do {
                try operation()
                call.resolve()
            } catch BackupFailure.occupiedTarget {
                call.reject("The local database slot is not empty", "BACKUP_TARGET")
            } catch {
                call.reject(message, code)
            }
        }
    }

    private func database() throws -> URL {
        try files.url(for: .documentDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
            .appendingPathComponent(databaseName)
    }

    private func candidate(_ call: CAPPluginCall) throws -> URL {
        guard let stage = call.getString("stageId"),
              stage.range(of: #"\A[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}\z"#, options: .regularExpression) != nil else {
            throw BackupFailure.invalidStage
        }
        let cache = try files.url(for: .cachesDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
            .resolvingSymlinksInPath()
        let path = cache.appendingPathComponent("pocket-device-backup").appendingPathComponent(stage)
            .appendingPathComponent("database.sqlite")
        guard path.resolvingSymlinksInPath().path == path.path else { throw BackupFailure.invalidStage }
        return path
    }

    private func size(_ path: URL) throws -> Int {
        let attrs = try files.attributesOfItem(atPath: path.path)
        guard attrs[.type] as? FileAttributeType == .typeRegular, let number = attrs[.size] as? Int else {
            throw BackupFailure.unfinishedDatabase
        }
        return number
    }

    private func requireEmpty(_ url: URL) throws {
        let names = try Set(files.contentsOfDirectory(atPath: url.deletingLastPathComponent().path))
        let reserved = [url.lastPathComponent] + sidecars.map { url.lastPathComponent + $0 }
        if reserved.contains(where: names.contains) { throw BackupFailure.occupiedTarget }
    }

    private func requireCheckpointed(_ url: URL) throws {
        guard try size(url) > 0 else { throw BackupFailure.unfinishedDatabase }
        for suffix in ["-wal", "-journal"] {
            let path = URL(fileURLWithPath: url.path + suffix)
            if files.fileExists(atPath: path.path), try size(path) != 0 {
                throw BackupFailure.unfinishedDatabase
            }
        }
    }

    private func validate(_ path: URL, password: String?) throws {
        guard let password, !password.isEmpty, !password.utf8.contains(0) else { throw BackupFailure.invalidDatabase }
        try requireCheckpointed(path)
        var connection: OpaquePointer?
        let status = sqlite3_open_v2(path.path, &connection, SQLITE_OPEN_READONLY | SQLITE_OPEN_FULLMUTEX, nil)
        defer { sqlite3_close(connection) }
        guard status == SQLITE_OK, let db = connection else { throw BackupFailure.invalidDatabase }

        // The bundled SQLCipher framework exposes PRAGMA key. SQLite string
        // literals escape an apostrophe by doubling it.
        let quotedKey = "'" + password.replacingOccurrences(of: "'", with: "''") + "'"
        guard sqlite3_exec(db, "PRAGMA key = " + quotedKey, nil, nil, nil) == SQLITE_OK,
              sqlite3_exec(db, "PRAGMA trusted_schema = OFF", nil, nil, nil) == SQLITE_OK else {
            throw BackupFailure.invalidDatabase
        }
        let tables = """
        SELECT count(*) FROM sqlite_master WHERE type='table' AND sql LIKE 'CREATE TABLE %' AND name IN
        ('local_field_crypto','local_notes','local_note_files','local_meetings','local_meeting_audio_parts','local_email_accounts','local_emails')
        """
        // LocalDB uses SQL migrations; the plugin can leave user_version at zero.
        let version = try scalar(db, "PRAGMA user_version")
        guard (version == "0" || version == "1"), try scalar(db, tables) == "7",
              try scalar(db, "PRAGMA quick_check(1)") == "ok",
              try scalar(db, "PRAGMA cipher_integrity_check") == nil,
              let salt = try scalar(db, "SELECT salt FROM local_field_crypto WHERE slot=1"),
              salt.utf8.count == 24, let bytes = Data(base64Encoded: salt),
              bytes.count == 16, bytes.base64EncodedString() == salt else {
            throw BackupFailure.invalidDatabase
        }
    }

    private func scalar(_ db: OpaquePointer, _ sql: String) throws -> String? {
        var statement: OpaquePointer?
        defer { sqlite3_finalize(statement) }
        guard sqlite3_prepare_v2(db, sql, -1, &statement, nil) == SQLITE_OK else {
            throw BackupFailure.invalidDatabase
        }
        let status = sqlite3_step(statement)
        if status == SQLITE_DONE { return nil }
        guard status == SQLITE_ROW, let bytes = sqlite3_column_text(statement, 0) else {
            throw BackupFailure.invalidDatabase
        }
        let result = String(cString: bytes)
        guard sqlite3_step(statement) == SQLITE_DONE else { throw BackupFailure.invalidDatabase }
        return result
    }
}
