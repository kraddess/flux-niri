package org.omarchy.flux

import android.app.Application
import android.content.ClipboardManager
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import org.omarchy.flux.core.Android
import org.omarchy.flux.core.ClipWatcher
import org.omarchy.flux.core.FluxCore
import org.omarchy.flux.core.Plugins

class FluxApp : Application() {
    private val clipListener = ClipboardManager.OnPrimaryClipChangedListener {
        if (FluxCore.foreground) Plugins.onLocalClipboard(FluxCore)
    }

    override fun onCreate() {
        super.onCreate()
        FluxCore.init(this)
        Android.createChannels(this)
        // Android 10 and later let an app read the clipboard only while it
        // has focus. In the background the listener only makes the system
        // log a denied read, and ClipWatcher answers that line.
        getSystemService(ClipboardManager::class.java)?.addPrimaryClipChangedListener(clipListener)
        ClipWatcher.start(this)
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) {
                FluxCore.foreground = true
                // Android 13 and later ask for log access while Flux is on
                // the screen, so a watcher that ended starts again here.
                ClipWatcher.start(this@FluxApp)
                // The user can change a permission or the network in the system settings.
                FluxCore.refresh()
            }

            override fun onStop(owner: LifecycleOwner) {
                FluxCore.foreground = false
            }
        })
    }
}
