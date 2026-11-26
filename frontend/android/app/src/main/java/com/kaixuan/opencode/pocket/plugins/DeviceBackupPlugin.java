package com.kaixuan.opencode.pocket.plugins;

import android.app.Activity;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.provider.DocumentsContract;
import android.system.Os;
import android.util.Base64;
import androidx.activity.result.ActivityResult;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.ActivityCallback;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.RandomAccessFile;
import java.security.MessageDigest;
import net.zetetic.database.sqlcipher.SQLiteDatabase;

@CapacitorPlugin(name = "DeviceBackup")
public class DeviceBackupPlugin extends Plugin {
    private static final String DATABASE_NAME = "lobsterSQLite.db";
    private static final String STAGING_ROOT = "pocket-device-backup";
    private static final String CANDIDATE_NAME = "database.sqlite";
    private static final String ARCHIVE_NAME = "OpenPocket-backup.pocketbackup";
    private static final long MAX_ARCHIVE_BYTES = (1L << 36) + (3L << 20);
    private static final String[] SIDECARS = { "-wal", "-shm", "-journal" };
    private static final String REQUIRED_TABLES = "SELECT count(*) FROM sqlite_master " +
        "WHERE type='table' AND sql LIKE 'CREATE TABLE %' AND name IN " +
        "('local_field_crypto','local_notes','local_note_files','local_meetings','local_meeting_audio_parts','local_email_accounts','local_emails')";

    private static final class OccupiedTarget extends IOException {}

    @Override
    public void load() {
        System.loadLibrary("sqlcipher");
    }

    @PluginMethod
    public void snapshotDatabase(PluginCall call) {
        try {
            File source = database();
            requireCheckpointed(source);
            File candidate = candidate(call);
            File parent = candidate.getParentFile();
            if ((!parent.isDirectory() && !parent.mkdirs()) || !candidate.createNewFile()) {
                throw new IOException("Could not reserve the snapshot path");
            }
            boolean copied = false;
            try (FileInputStream input = new FileInputStream(source); FileOutputStream output = new FileOutputStream(candidate)) {
                byte[] buffer = new byte[128 * 1024];
                int size;
                while ((size = input.read(buffer)) != -1) output.write(buffer, 0, size);
                output.getFD().sync();
                requireCheckpointed(source);
                if (candidate.length() != source.length()) throw new IOException("Snapshot size changed");
                copied = true;
            } finally {
                if (!copied) candidate.delete();
            }
            call.resolve();
        } catch (Exception error) {
            call.reject("Unable to copy the checkpointed local database", "BACKUP_SNAPSHOT");
        }
    }

    @PluginMethod
    public void validateDatabase(PluginCall call) {
        try {
            File file = candidate(call);
            validate(file, call.getString("password"));
            JSObject result = new JSObject();
            if (Boolean.TRUE.equals(call.getBoolean("includeDigest", false))) result.put("digest", digest(file));
            call.resolve(result);
        } catch (Exception error) {
            call.reject("The backup database cannot be verified", "BACKUP_DATABASE");
        }
    }

    @PluginMethod
    public void databaseRestoreStatus(PluginCall call) {
        String expected = call.getString("digest");
        if (expected == null || !expected.matches("[0-9a-f]{64}")) {
            call.reject("Invalid restore identity", "BACKUP_STATE");
            return;
        }
        try {
            File current = database();
            String status;
            try {
                requireEmpty(current);
                status = "empty";
            } catch (OccupiedTarget occupied) {
                status = current.isFile() && expected.equals(digest(current)) ? "published" : "conflict";
            }
            call.resolve(new JSObject().put("status", status));
        } catch (Exception error) {
            call.reject("Cannot determine the installed database identity", "BACKUP_STATE");
        }
    }

    @PluginMethod
    public void pickArchive(PluginCall call) {
        try {
            stagedFile(call, ARCHIVE_NAME);
            Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT);
            intent.addCategory(Intent.CATEGORY_OPENABLE);
            intent.setType("*/*");
            startActivityForResult(call, intent, "archivePicked");
        } catch (Exception error) {
            call.reject("Unable to open the system document picker", "BACKUP_FILE");
        }
    }

    @ActivityCallback
    private void archivePicked(PluginCall call, ActivityResult result) {
        transferArchive(call, result, false);
    }

    @PluginMethod
    public void saveArchive(PluginCall call) {
        try {
            File file = stagedFile(call, ARCHIVE_NAME);
            if (!file.isFile() || file.length() == 0 || file.length() > MAX_ARCHIVE_BYTES) throw new IOException("Invalid archive");
            Intent intent = new Intent(Intent.ACTION_CREATE_DOCUMENT);
            intent.addCategory(Intent.CATEGORY_OPENABLE);
            intent.setType("application/octet-stream");
            intent.putExtra(Intent.EXTRA_TITLE, ARCHIVE_NAME);
            startActivityForResult(call, intent, "archiveSaved");
        } catch (Exception error) {
            call.reject("Unable to open the system save dialog", "BACKUP_FILE");
        }
    }

    @ActivityCallback
    private void archiveSaved(PluginCall call, ActivityResult result) {
        transferArchive(call, result, true);
    }

    private void transferArchive(PluginCall call, ActivityResult result, boolean save) {
        if (call == null) return;
        Uri uri = result.getData() == null ? null : result.getData().getData();
        if (result.getResultCode() == Activity.RESULT_CANCELED) {
            call.resolve(new JSObject().put("completed", false));
            return;
        }
        if (result.getResultCode() != Activity.RESULT_OK || uri == null || !"content".equals(uri.getScheme())) {
            call.reject("The system did not return a usable document", "BACKUP_FILE");
            return;
        }
        getBridge().execute(() -> {
            try {
                File file = stagedFile(call, ARCHIVE_NAME);
                if (save) {
                    try (InputStream input = new FileInputStream(file);
                         OutputStream output = getContext().getContentResolver().openOutputStream(uri, "wt")) {
                        copyArchive(input, output);
                    }
                } else {
                    File parent = file.getParentFile();
                    if ((!parent.isDirectory() && !parent.mkdirs()) || !file.createNewFile()) throw new IOException("Cannot reserve archive path");
                    try (InputStream input = getContext().getContentResolver().openInputStream(uri);
                         OutputStream output = new FileOutputStream(file)) {
                        copyArchive(input, output);
                    } catch (Exception error) {
                        file.delete();
                        throw error;
                    }
                }
                call.resolve(new JSObject().put("completed", true));
            } catch (Exception error) {
                if (save) {
                    try { DocumentsContract.deleteDocument(getContext().getContentResolver(), uri); } catch (Exception ignored) {}
                }
                call.reject("Unable to transfer the backup document", "BACKUP_FILE");
            }
        });
    }

    private static void copyArchive(InputStream input, OutputStream output) throws IOException {
        if (input == null || output == null) throw new IOException("Document stream unavailable");
        byte[] buffer = new byte[128 * 1024];
        long transferred = 0;
        int size;
        while ((size = input.read(buffer)) != -1) {
            transferred += size;
            if (transferred > MAX_ARCHIVE_BYTES) throw new IOException("Archive exceeds the supported size");
            output.write(buffer, 0, size);
        }
        if (transferred == 0) throw new IOException("Empty archive");
    }

    @PluginMethod
    public void ensureEmptyDatabase(PluginCall call) {
        try {
            requireEmpty(database());
            call.resolve();
        } catch (Exception error) {
            call.reject("The local database slot is not empty", "BACKUP_TARGET");
        }
    }

    @PluginMethod
    public void publishDatabase(PluginCall call) {
        File destination = database();
        try {
            requireEmpty(destination);
            File source = candidate(call);
            validate(source, call.getString("password"));
            try (RandomAccessFile file = new RandomAccessFile(source, "r")) {
                file.getFD().sync();
            }
            // The LocalDB queue excludes application opens while we check and
            // rename. Android app SELinux policy does not permit hard links.
            requireEmpty(destination);
            Os.rename(source.getPath(), destination.getPath());
            call.resolve();
        } catch (OccupiedTarget error) {
            call.reject("The local database slot is not empty", "BACKUP_TARGET");
        } catch (Exception error) {
            call.reject("Unable to publish the verified backup database", "BACKUP_PUBLISH");
        }
    }

    private File database() {
        return getContext().getDatabasePath(DATABASE_NAME);
    }

    private File candidate(PluginCall call) throws IOException {
        return stagedFile(call, CANDIDATE_NAME);
    }

    private File stagedFile(PluginCall call, String name) throws IOException {
        String id = call.getString("stageId");
        if (id == null || !id.matches("[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}")) {
            throw new IOException("Invalid backup stage ID");
        }
        File root = getContext().getCacheDir().getCanonicalFile();
        File path = new File(root, STAGING_ROOT + "/" + id + "/" + name);
        if (!path.equals(path.getCanonicalFile())) throw new IOException("Invalid backup stage");
        return path;
    }

    private static String digest(File file) throws Exception {
        MessageDigest sha = MessageDigest.getInstance("SHA-256");
        try (FileInputStream input = new FileInputStream(file)) {
            byte[] buffer = new byte[128 * 1024];
            int size;
            while ((size = input.read(buffer)) != -1) sha.update(buffer, 0, size);
        }
        StringBuilder result = new StringBuilder(64);
        for (byte value : sha.digest()) {
            result.append(Character.forDigit((value & 0xff) >>> 4, 16));
            result.append(Character.forDigit(value & 0xf, 16));
        }
        return result.toString();
    }

    private static void requireEmpty(File file) throws IOException {
        File parent = file.getParentFile();
        if (!parent.exists() && parent.mkdirs()) return;
        String[] entries = parent.list();
        if (entries == null) throw new IOException("Cannot inspect database directory");
        for (String entry : entries) {
            if (entry.equals(file.getName())) throw new OccupiedTarget();
            for (String suffix : SIDECARS) {
                if (entry.equals(file.getName() + suffix)) throw new OccupiedTarget();
            }
        }
    }

    private static void requireCheckpointed(File file) throws IOException {
        if (!file.isFile() || file.length() == 0 || new File(file.getPath() + "-wal").length() != 0 ||
            new File(file.getPath() + "-journal").length() != 0) {
            throw new IOException("The database requires a complete checkpoint");
        }
    }

    private static void validate(File file, String password) throws IOException {
        if (password == null || password.isEmpty()) throw new IOException("Missing master password");
        requireCheckpointed(file);
        try (SQLiteDatabase db = SQLiteDatabase.openDatabase(file.getPath(), password, null, SQLiteDatabase.OPEN_READONLY, null)) {
            db.rawExecSQL("PRAGMA trusted_schema = OFF");
            // LocalDB uses SQL migrations; the plugin can leave user_version at zero.
            String version = scalar(db, "PRAGMA user_version");
            if ((!"0".equals(version) && !"1".equals(version)) ||
                !"7".equals(scalar(db, REQUIRED_TABLES)) ||
                !"ok".equals(scalar(db, "PRAGMA quick_check(1)"))) {
                throw new IOException("Invalid database schema");
            }
            try (Cursor integrity = db.rawQuery("PRAGMA cipher_integrity_check", new String[0])) {
                if (integrity.moveToFirst()) throw new IOException("Invalid SQLCipher page authentication");
            }
            String salt = scalar(db, "SELECT salt FROM local_field_crypto WHERE slot=1");
            if (salt == null || salt.length() != 24) throw new IOException("Invalid field salt");
            byte[] bytes = Base64.decode(salt, Base64.NO_WRAP);
            if (bytes.length != 16 || !Base64.encodeToString(bytes, Base64.NO_WRAP).equals(salt)) {
                throw new IOException("Invalid field salt");
            }
        }
    }

    private static String scalar(SQLiteDatabase db, String sql) throws IOException {
        try (Cursor rows = db.rawQuery(sql, new String[0])) {
            if (!rows.moveToFirst() || rows.isNull(0)) throw new IOException("Missing database validation result");
            String value = rows.getString(0);
            if (rows.moveToNext()) throw new IOException("Ambiguous database validation result");
            return value;
        }
    }
}
