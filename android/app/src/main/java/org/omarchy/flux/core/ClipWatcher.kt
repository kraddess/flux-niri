package org.omarchy.flux.core

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.util.Log
import org.omarchy.flux.ui.ClipReadActivity

/**
 * Sends a copy that another app makes while Flux is in the background.
 *
 * Android 10 and later let only the app with focus read the clipboard. The
 * clipboard service still tells a background listener about each change,
 * but it denies the read and logs "Denied clipboard access to <package>".
 * With READ_LOGS, granted once with adb, Flux watches the log for that line
 * and opens [ClipReadActivity], an invisible window that takes focus for a
 * moment, reads the clipboard, and sends it. Starting that window from the
 * background needs the "Display over other apps" permission.
 */
object ClipWatcher {
    private const val TAG = "FluxClipWatcher"

    /** A copy from the computer changes the clipboard too. Flux skips the read that it causes. */
    @Volatile var remoteSetAt = 0L

    @Volatile private var thread: Thread? = null
    @Volatile private var lastLaunch = 0L

    fun granted(ctx: Context): Boolean =
        ctx.checkSelfPermission(Manifest.permission.READ_LOGS) == PackageManager.PERMISSION_GRANTED

    /** Starts the watcher when READ_LOGS is granted. A running watcher keeps running. */
    @Synchronized
    fun start(ctx: Context) {
        if (thread?.isAlive == true || !granted(ctx)) return
        val app = ctx.applicationContext
        val needle = "Denied clipboard access to ${app.packageName}"
        thread = Thread({ watch(app, needle) }, TAG).apply {
            isDaemon = true
            start()
        }
    }

    private fun watch(app: Context, needle: String) {
        // -T 1 starts at the newest line, so older denials do not count.
        val proc = runCatching {
            ProcessBuilder("logcat", "-T", "1", "-v", "brief", "ClipboardService:E", "*:S")
                .redirectErrorStream(true).start()
        }.getOrElse {
            Log.w(TAG, "cannot start logcat: $it")
            return
        }
        Log.i(TAG, "watching the clipboard")
        val started = System.currentTimeMillis()
        try {
            proc.inputStream.bufferedReader().forEachLine { line ->
                if (needle in line && System.currentTimeMillis() - started > 1000) onDenied(app)
            }
        } finally {
            proc.destroy()
            Log.i(TAG, "logcat ended")
        }
    }

    private fun onDenied(app: Context) {
        val now = System.currentTimeMillis()
        if (!FluxCore.settings.syncClipboard || FluxCore.foreground) return
        if (now - remoteSetAt < 2000 || now - lastLaunch < 1000) return
        if (FluxCore.connectedPaired().isEmpty()) return
        lastLaunch = now
        runCatching {
            app.startActivity(
                Intent(app, ClipReadActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_ANIMATION),
            )
        }.onFailure { Log.w(TAG, "cannot open the clipboard reader: $it") }
    }
}
