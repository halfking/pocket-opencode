import Capacitor
import CryptoKit
import Darwin
import Foundation
import SQLCipher
import UIKit
import UniformTypeIdentifiers

final class PocketBridgeViewController: CAPBridgeViewController {
    override func capacitorDidLoad() {
        super.capacitorDidLoad()
        bridge?.registerPluginInstance(DeviceBackupPlugin())
    }
}

@objc(DeviceBackupPlugin)
final class DeviceBackupPlugin: CAPPlugin, CAPBridgedPlugin, UIDocumentPickerDelegate {
    let identifier = "DeviceBackupPlugin"
    let jsName = "DeviceBackup"
    let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "snapshotDatabase", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "validateDatabase", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "databaseRestoreStatus", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "ensureEmptyDatabase", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "publishDatabase", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "pickArchive", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "saveArchive", returnType: CAPPluginReturnPromise)
    ]

    private let queue = DispatchQueue(label: "pocket.device-backup")
    private let files = FileManager.default
    private let databaseName = "lobsterSQLite.db"
    private let archiveName = "OpenPocket-backup.pocketbackup"
    private let archiveLimit = (1 << 36) + (3 << 20)
    private let sidecars = ["-wal", "-shm", "-journal"]
    private enum BackupFailure: Error { case invalidStage, unfinishedDatabase, invalidDatabase, occupiedTarget }
    private var pickerRequest: (call: CAPPluginCall, file: URL, save: Bool)?

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
        resolve(call, code: "BACKUP_DATABASE", message: "The backup database cannot be verified") {
            let file = try self.candidate(call)
            try self.validate(file, password: call.getString("password"))
            if call.getBool("includeDigest") == true { return ["digest": try self.digest(file)] }
            return [:]
        }
    }

    @objc func databaseRestoreStatus(_ call: CAPPluginCall) {
        resolve(call, code: "BACKUP_STATE", message: "Cannot determine the installed database identity") {
            guard let expected = call.getString("digest"),
                  expected.range(of: #"\A[0-9a-f]{64}\z"#, options: .regularExpression) != nil else {
                throw BackupFailure.invalidStage
            }
            let current = try self.database()
            do {
                try self.requireEmpty(current)
                return ["status": "empty"]
            } catch BackupFailure.occupiedTarget {
                let matches = self.files.fileExists(atPath: current.path) && (try? self.digest(current)) == expected
                return ["status": matches ? "published" : "conflict"]
            }
        }
    }

    @objc func pickArchive(_ call: CAPPluginCall) {
        presentPicker(call, save: false)
    }

    @objc func saveArchive(_ call: CAPPluginCall) {
        presentPicker(call, save: true)
    }

    private func presentPicker(_ call: CAPPluginCall, save: Bool) {
        DispatchQueue.main.async {
            guard self.pickerRequest == nil, let presenter = self.bridge?.viewController,
                  presenter.presentedViewController == nil else {
                call.reject("The system document dialog is busy", "BACKUP_FILE")
                return
            }
            do {
                let file = try self.stagedFile(call, name: self.archiveName)
                let picker: UIDocumentPickerViewController
                if save {
                    let size = try self.size(file)
                    guard size > 0 && size <= self.archiveLimit else { throw BackupFailure.invalidStage }
                    picker = UIDocumentPickerViewController(forExporting: [file], asCopy: true)
                } else {
                    picker = UIDocumentPickerViewController(forOpeningContentTypes: [.data], asCopy: false)
                }
                self.pickerRequest = (call, file, save)
                picker.delegate = self
                presenter.present(picker, animated: true)
            } catch {
                call.reject("Unable to open the system document dialog", "BACKUP_FILE")
            }
        }
    }

    func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
        let call = pickerRequest?.call
        pickerRequest = nil
        call?.resolve(["completed": false])
    }

    func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
        guard let request = pickerRequest else { return }
        pickerRequest = nil
        guard let url = urls.first else {
            request.call.reject("The system did not return a usable document", "BACKUP_FILE")
            return
        }
        if request.save {
            request.call.resolve(["completed": true])
            return
        }
        resolve(request.call, code: "BACKUP_FILE", message: "Unable to read the selected backup document") {
            let accessed = url.startAccessingSecurityScopedResource()
            defer { if accessed { url.stopAccessingSecurityScopedResource() } }
            let coordinator = NSFileCoordinator()
            var coordinationError: NSError?
            var copyError: Error?
            coordinator.coordinate(readingItemAt: url, options: [], error: &coordinationError) { file in
                do { try self.copyArchive(file, to: request.file) }
                catch { copyError = error }
            }
            if let error = coordinationError ?? copyError { throw error }
            return ["completed": true]
        }
    }

    private func copyArchive(_ source: URL, to destination: URL) throws {
        let length = try size(source)
        guard length > 0 && length <= archiveLimit, !files.fileExists(atPath: destination.path) else {
            throw BackupFailure.invalidStage
        }
        try files.createDirectory(at: destination.deletingLastPathComponent(), withIntermediateDirectories: true)
        guard files.createFile(atPath: destination.path, contents: nil) else { throw BackupFailure.invalidStage }
        do {
            let from = try FileHandle(forReadingFrom: source)
            defer { try? from.close() }
            let to = try FileHandle(forWritingTo: destination)
            defer { try? to.close() }
            var transferred = 0
            while let data = try from.read(upToCount: 128 * 1024), !data.isEmpty {
                transferred += data.count
                guard transferred <= archiveLimit else { throw BackupFailure.invalidStage }
                try to.write(contentsOf: data)
            }
            guard transferred == length else { throw BackupFailure.invalidStage }
        } catch {
            try? files.removeItem(at: destination)
            throw error
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
        resolve(call, code: code, message: message) {
            try operation()
            return [:]
        }
    }

    private func resolve(_ call: CAPPluginCall, code: String, message: String, operation: @escaping () throws -> [String: Any]) {
        queue.async {
            do {
                call.resolve(try operation())
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
        try stagedFile(call, name: "database.sqlite")
    }

    private func stagedFile(_ call: CAPPluginCall, name: String) throws -> URL {
        guard let stage = call.getString("stageId"),
              stage.range(of: #"\A[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}\z"#, options: .regularExpression) != nil else {
            throw BackupFailure.invalidStage
        }
        let cache = try files.url(for: .cachesDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
            .resolvingSymlinksInPath()
        let path = cache.appendingPathComponent("pocket-device-backup").appendingPathComponent(stage)
            .appendingPathComponent(name)
        guard path.resolvingSymlinksInPath().path == path.path else { throw BackupFailure.invalidStage }
        return path
    }

    private func digest(_ file: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: file)
        defer { try? handle.close() }
        var sha = SHA256()
        while let data = try handle.read(upToCount: 128 * 1024), !data.isEmpty { sha.update(data: data) }
        return sha.finalize().map { String(format: "%02x", $0) }.joined()
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
