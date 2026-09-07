package com.kaixuan.opencode.pocket.plugins;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyPermanentlyInvalidatedException;
import android.security.keystore.KeyProperties;
import android.util.Base64;
import androidx.biometric.BiometricManager;
import androidx.biometric.BiometricPrompt;
import androidx.core.content.ContextCompat;
import androidx.fragment.app.FragmentActivity;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

/**
 * 指纹/人脸门 + Keystore AES-GCM 密文。登录凭据读写都弹 BiometricPrompt；
 * 主密码仅读取弹窗（写入在密码解锁成功后静默完成）。不用 CryptoObject：
 * Class 2 设备不支持 per-op auth 绑定。
 */
@CapacitorPlugin(name = "BiometricAuth")
public class BiometricAuthPlugin extends Plugin {
    private static final String PREFS = "biometric_auth";
    private static final String KEY_CRED = "cred_blob";
    private static final String KEY_MASTER = "master_blob";
    // v2：v1 曾用 setUserAuthenticationRequired(true)（CryptoObject 方案），
    // Keystore 残留的旧密钥会抛 UserNotAuthenticated；换别名一次重建。
    private static final String KEYSTORE_ALIAS = "pocket_biometric_key_v2";
    private static final String ANDROID_KEYSTORE = "AndroidKeyStore";
    private static final String TRANSFORM = "AES/GCM/NoPadding";
    private static final char SEP = '\u0000';

    @PluginMethod
    public void isAvailable(PluginCall call) {
        JSObject ret = new JSObject();
        try {
            int can = BiometricManager.from(getContext())
                    .canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_WEAK);
            ret.put("available", can == BiometricManager.BIOMETRIC_SUCCESS);
            ret.put("code", can);
        } catch (Exception e) {
            ret.put("available", false);
        }
        call.resolve(ret);
    }

    @PluginMethod
    public void hasCredential(PluginCall call) {
        SharedPreferences prefs = prefs();
        JSObject ret = new JSObject();
        ret.put("has", prefs.contains(KEY_CRED));
        call.resolve(ret);
    }

    /** 绑定：弹一次指纹验证，通过后加密存储凭据。 */
    @PluginMethod
    public void saveCredential(PluginCall call) {
        String username = call.getString("username", "");
        String password = call.getString("password", "");
        if (username.isEmpty() || password.isEmpty() || username.indexOf(SEP) >= 0
                || password.indexOf(SEP) >= 0) {
            call.reject("invalid username or password");
            return;
        }
        FragmentActivity activity = fragmentActivity();
        if (activity == null) {
            call.reject("biometric not supported on this platform");
            return;
        }
        withBiometricCipher(call, activity, Cipher.ENCRYPT_MODE, null, cipher -> {
            byte[] ct = cipher.doFinal(
                    (username + SEP + password).getBytes(StandardCharsets.UTF_8));
            String blob = Base64.encodeToString(cipher.getIV(), Base64.NO_WRAP) + ":"
                    + Base64.encodeToString(ct, Base64.NO_WRAP);
            prefs().edit().putString(KEY_CRED, blob).apply();
            call.resolve();
        });
    }

    /** 指纹登录：验证通过后解密返回凭据。 */
    @PluginMethod
    public void getCredential(PluginCall call) {
        String[] ivAndCt = blob();
        if (ivAndCt == null) {
            call.reject("no credential bound");
            return;
        }
        FragmentActivity activity = fragmentActivity();
        if (activity == null) {
            call.reject("biometric not supported on this platform");
            return;
        }
        withBiometricCipher(call, activity, Cipher.DECRYPT_MODE, ivAndCt[0], cipher -> {
            byte[] pt = cipher.doFinal(Base64.decode(ivAndCt[1], Base64.NO_WRAP));
            String joined = new String(pt, StandardCharsets.UTF_8);
            int sep = joined.indexOf(SEP);
            if (sep <= 0 || sep == joined.length() - 1) {
                wipe();
                call.reject("credential corrupted; please rebind");
                return;
            }
            JSObject ret = new JSObject();
            ret.put("username", joined.substring(0, sep));
            ret.put("password", joined.substring(sep + 1));
            call.resolve(ret);
        });
    }

    /** 解绑。 */
    @PluginMethod
    public void deleteCredential(PluginCall call) {
        wipe();
        call.resolve();
    }

    @PluginMethod
    public void hasMasterSecret(PluginCall call) {
        JSObject ret = new JSObject();
        ret.put("has", prefs().contains(KEY_MASTER));
        call.resolve(ret);
    }

    /** 密码解锁成功后静默写入；读取仍走 BiometricPrompt。 */
    @PluginMethod
    public void saveMasterSecret(PluginCall call) {
        String password = call.getString("password", "");
        if (password.isEmpty()) {
            call.reject("invalid master password");
            return;
        }
        try {
            ensureKey();
            Cipher cipher = Cipher.getInstance(TRANSFORM);
            cipher.init(Cipher.ENCRYPT_MODE, loadKey());
            byte[] ct = cipher.doFinal(password.getBytes(StandardCharsets.UTF_8));
            String blob = Base64.encodeToString(cipher.getIV(), Base64.NO_WRAP) + ":"
                    + Base64.encodeToString(ct, Base64.NO_WRAP);
            prefs().edit().putString(KEY_MASTER, blob).apply();
            call.resolve();
        } catch (Exception e) {
            call.reject("crypto failed: " + e.getMessage());
        }
    }

    @PluginMethod
    public void getMasterSecret(PluginCall call) {
        String[] ivAndCt = blob(KEY_MASTER);
        if (ivAndCt == null) {
            call.reject("no master secret bound");
            return;
        }
        FragmentActivity activity = fragmentActivity();
        if (activity == null) {
            call.reject("biometric not supported on this platform");
            return;
        }
        withBiometricCipher(call, activity, Cipher.DECRYPT_MODE, ivAndCt[0], cipher -> {
            byte[] pt = cipher.doFinal(Base64.decode(ivAndCt[1], Base64.NO_WRAP));
            JSObject ret = new JSObject();
            ret.put("password", new String(pt, StandardCharsets.UTF_8));
            call.resolve(ret);
        });
    }

    // ---- internals ----

    private interface CryptoTask {
        void run(Cipher cipher) throws Exception;
    }

    /**
     * 初始化指定模式的 Cipher，经 BiometricPrompt 认证（无 CryptoObject，见类注释）
     * 后执行 task。认证失败/密钥失效统一 reject。
     */
    private void withBiometricCipher(PluginCall call, FragmentActivity activity,
                                     int opmode, String ivB64, CryptoTask task) {
        try {
            ensureKey();
            Cipher cipher = Cipher.getInstance(TRANSFORM);
            if (opmode == Cipher.ENCRYPT_MODE) {
                cipher.init(opmode, loadKey());
            } else {
                cipher.init(opmode, loadKey(),
                        new GCMParameterSpec(128, Base64.decode(ivB64, Base64.NO_WRAP)));
            }
            // Capacitor 桥线程非主线程；BiometricPrompt 必须在 FragmentActivity 的主线程创建并 authenticate
            activity.runOnUiThread(() -> {
                try {
                    showPrompt(call, activity, cipher, task);
                } catch (Exception e) {
                    call.reject("init failed: " + e.getMessage());
                }
            });
        } catch (KeyPermanentlyInvalidatedException kpe) {
            wipe();
            call.reject("biometric key invalidated; please rebind with password");
        } catch (Exception e) {
            call.reject("init failed: " + e.getMessage());
        }
    }

    private void showPrompt(PluginCall call, FragmentActivity activity,
                            Cipher cipher, CryptoTask task) {
            BiometricPrompt prompt = new BiometricPrompt(activity,
                    ContextCompat.getMainExecutor(getContext()),
                    new BiometricPrompt.AuthenticationCallback() {
                        @Override
                        public void onAuthenticationSucceeded(
                                BiometricPrompt.AuthenticationResult result) {
                            try {
                                task.run(cipher);
                            } catch (KeyPermanentlyInvalidatedException kpe) {
                                wipe();
                                call.reject("biometric key invalidated; please rebind with password");
                            } catch (Exception e) {
                                call.reject("crypto failed: " + e.getMessage());
                            }
                        }

                        @Override
                        public void onAuthenticationError(int code, CharSequence errString) {
                            call.reject("biometric error " + code + ": " + errString);
                        }
                    });
            BiometricPrompt.PromptInfo info = new BiometricPrompt.PromptInfo.Builder()
                    .setTitle("OpenCode Pocket")
                    .setSubtitle(call.getString("reason", "验证身份"))
                    .setNegativeButtonText("取消")
                    .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_WEAK)
                    .build();
            prompt.authenticate(info);
    }

    private FragmentActivity fragmentActivity() {
        return getActivity() instanceof FragmentActivity ? (FragmentActivity) getActivity() : null;
    }

    private synchronized void ensureKey() throws Exception {
        KeyStore ks = KeyStore.getInstance(ANDROID_KEYSTORE);
        ks.load(null);
        if (ks.containsAlias(KEYSTORE_ALIAS)) return;
        KeyGenerator kg = KeyGenerator.getInstance(
                KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE);
        kg.init(new KeyGenParameterSpec.Builder(KEYSTORE_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build());
        kg.generateKey();
    }

    private SecretKey loadKey() throws Exception {
        KeyStore ks = KeyStore.getInstance(ANDROID_KEYSTORE);
        ks.load(null);
        SecretKey key = (SecretKey) ks.getKey(KEYSTORE_ALIAS, null);
        if (key == null) {
            // 别名丢失（清除数据/系统还原）：重建密钥并作废旧凭据（IV 对不上）
            wipe();
            ensureKey();
            key = (SecretKey) ks.getKey(KEYSTORE_ALIAS, null);
        }
        return key;
    }

    /** 返回 [iv, ciphertext]；无凭据或格式损坏返回 null（损坏时清该 key）。 */
    private String[] blob() {
        return blob(KEY_CRED);
    }

    private String[] blob(String key) {
        String blob = prefs().getString(key, null);
        if (blob == null) return null;
        String[] parts = blob.split(":", 2);
        if (parts.length != 2 || parts[0].isEmpty() || parts[1].isEmpty()) {
            prefs().edit().remove(key).apply();
            return null;
        }
        return parts;
    }

    private SharedPreferences prefs() {
        return getContext().getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    private void wipe() {
        prefs().edit().clear().apply();
    }
}
