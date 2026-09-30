// flux-gui is the Qt6 window of Flux. It shows the same QML views as the
// Omarchy shell plugin and talks to fluxd over its IPC socket.

#include <QApplication>
#include <QCommandLineParser>
#include <QDir>
#include <QGuiApplication>
#include <QIcon>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QMenu>
#include <QProcess>
#include <QSystemTrayIcon>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickWindow>
#include <QTimer>
#include <QUrl>

#include <vector>

#include "fluxbackend.h"
#include "selfwatch.h"
#include "singleinstance.h"
#include "themewatcher.h"

namespace {

// qmlBase returns the folder of the shared views: the resources, or
// FLUX_QML_DIR for development.
QString qmlBase()
{
    const QString dir = qEnvironmentVariable("FLUX_QML_DIR");
    if (!dir.isEmpty())
        return QUrl::fromLocalFile(QDir(dir).absolutePath()).toString();
    return QStringLiteral("qrc:/flux/qml");
}

FluxBackend *expose(QQmlApplicationEngine &engine)
{
    auto *backend = new FluxBackend(&engine, &engine);
    QQmlContext *ctx = engine.rootContext();
    ctx->setContextProperty(QStringLiteral("fluxQmlBase"), qmlBase());
    ctx->setContextProperty(QStringLiteral("fluxTheme"), new ThemeWatcher(&engine));
    ctx->setContextProperty(QStringLiteral("fluxBackend"), backend);
    return backend;
}

// focusNiri asks niri to focus the window of this process. niri has no
// rule by app id, so it looks up the window id by the process id.
bool focusNiri()
{
    if (qEnvironmentVariableIsEmpty("NIRI_SOCKET"))
        return false;
    QProcess list;
    list.start(QStringLiteral("niri"), {QStringLiteral("msg"), QStringLiteral("-j"), QStringLiteral("windows")});
    if (!list.waitForFinished(1000))
        return false;
    const qint64 pid = QCoreApplication::applicationPid();
    for (const QJsonValue &w : QJsonDocument::fromJson(list.readAllStandardOutput()).array()) {
        const QJsonObject o = w.toObject();
        if (o.value(QStringLiteral("pid")).toInteger() == pid) {
            QProcess::startDetached(QStringLiteral("niri"),
                                    {QStringLiteral("msg"), QStringLiteral("action"), QStringLiteral("focus-window"),
                                     QStringLiteral("--id"), QString::number(o.value(QStringLiteral("id")).toInteger())});
            return true;
        }
    }
    return false;
}

// raise brings the window to the front. Wayland lets a window take focus
// only with an activation token. Without a token, flux-gui asks niri or
// Hyprland.
void raise(QQuickWindow *window, const QString &token)
{
    window->show();
    window->raise();
    if (!token.isEmpty()) {
        qputenv("XDG_ACTIVATION_TOKEN", token.toUtf8());
        window->requestActivate();
        return;
    }
    // A window that has just been shown gets its niri id a moment later.
    if (!qEnvironmentVariableIsEmpty("NIRI_SOCKET")) {
        QTimer::singleShot(150, window, [] { focusNiri(); });
        return;
    }
    QProcess::startDetached(QStringLiteral("hyprctl"),
                            {QStringLiteral("dispatch"), QStringLiteral("focuswindow"), QStringLiteral("class:^flux$")});
}

// addTray puts the Flux icon into the system tray. A click opens the
// window. Closing the window then hides it, and flux-gui keeps running in
// the tray. Quit Flux turns fluxd off and ends flux-gui.
void addTray(QApplication &app, QQuickWindow *window, FluxBackend *backend)
{
    if (!QSystemTrayIcon::isSystemTrayAvailable())
        return;
    app.setQuitOnLastWindowClosed(false);
    auto *tray = new QSystemTrayIcon(QGuiApplication::windowIcon(), &app);
    tray->setToolTip(QStringLiteral("Flux"));
    auto *menu = new QMenu();
    QObject::connect(&app, &QCoreApplication::aboutToQuit, menu, &QObject::deleteLater);
    auto open = [window, backend] {
        backend->retryNow();
        raise(window, QString());
    };
    menu->addAction(QStringLiteral("Open Flux"), open);
    QAction *start = menu->addAction(QStringLiteral("Start Flux"), [] {
        QProcess::startDetached(QStringLiteral("flux-cli"), {QStringLiteral("on")});
    });
    auto update = [start, backend] { start->setVisible(!backend->connected()); };
    QObject::connect(backend, &FluxBackend::connectedChanged, start, update);
    update();
    menu->addSeparator();
    menu->addAction(QStringLiteral("Quit Flux"), &app, [&app] {
        QProcess::execute(QStringLiteral("flux-cli"), {QStringLiteral("off")});
        app.quit();
    });
    tray->setContextMenu(menu);
    QObject::connect(tray, &QSystemTrayIcon::activated, window, [open](QSystemTrayIcon::ActivationReason r) {
        if (r == QSystemTrayIcon::Trigger || r == QSystemTrayIcon::DoubleClick)
            open();
    });
    tray->show();
}

// snapshot renders every screen of the shared views with the mock backend
// into PNG files. Snapshot.qml reads the arguments after "--", so argv is
// rebuilt as "flux-gui -- DIR [ONLY]" before Qt reads it.
int snapshot(int argc, char *argv[])
{
    std::vector<char *> args{argv[0], const_cast<char *>("--")};
    for (int i = 1; i < argc; i++) {
        if (qstrcmp(argv[i], "--snapshot") != 0)
            args.push_back(argv[i]);
    }
    int count = int(args.size());
    args.push_back(nullptr);

    QGuiApplication::setDesktopFileName(QStringLiteral("flux"));
    QGuiApplication app(count, args.data());
    // The harness reads its fixture and the environment with XMLHttpRequest.
    qputenv("QML_XHR_ALLOW_FILE_READ", "1");
    QQmlApplicationEngine engine;
    engine.load(QUrl(qmlBase() + QStringLiteral("/tools/Snapshot.qml")));
    if (engine.rootObjects().isEmpty())
        return 1;
    return app.exec();
}

// grabAfterState saves the window into a PNG file after the first state
// from fluxd, and then quits. It proves the whole path from fluxd to the
// pixels. Set FLUX_GUI_GRAB=<file.png> to use it.
void grabAfterState(QQuickWindow *window, FluxBackend *backend, const QString &file)
{
    auto save = [window, backend, file] {
        const int devices = backend->devices().property(QStringLiteral("length")).toInt();
        qInfo("flux-gui: connected=%d devices=%d", backend->connected(), devices);
        const bool ok = window->grabWindow().save(file);
        qInfo("flux-gui: %s %s", ok ? "saved" : "cannot save", qPrintable(file));
        QCoreApplication::exit(ok ? 0 : 1);
    };
    QObject::connect(backend, &FluxBackend::stateChanged, window, [save] { QTimer::singleShot(1500, save); },
                     Qt::SingleShotConnection);
    QTimer::singleShot(8000, window, save);
}

} // namespace

int main(int argc, char *argv[])
{
    for (int i = 1; i < argc; i++) {
        if (qstrcmp(argv[i], "--snapshot") == 0)
            return snapshot(argc, argv);
    }

    QGuiApplication::setDesktopFileName(QStringLiteral("flux"));
    QApplication app(argc, argv);
    QGuiApplication::setApplicationName(QStringLiteral("flux"));
    // The icon theme gives the icon through flux.desktop. The embedded icon
    // is for a system with no installed flux icon.
    QGuiApplication::setWindowIcon(QIcon::fromTheme(QStringLiteral("flux"), QIcon(QStringLiteral(":/flux/icons/flux.svg"))));
    QGuiApplication::setApplicationVersion(QStringLiteral(FLUX_VERSION));

    QCommandLineParser parser;
    parser.setApplicationDescription(QStringLiteral("The Flux window. It connects to fluxd."));
    parser.addHelpOption();
    parser.addVersionOption();
    // --snapshot is handled before the parser runs. It is listed for --help.
    parser.addOption({QStringLiteral("snapshot"),
                      QStringLiteral("Render every screen with test data into PNG files in <dir>, then quit."),
                      QStringLiteral("dir")});
    parser.addOption({QStringLiteral("hidden"),
                      QStringLiteral("Start in the system tray without showing the window.")});
    parser.addPositionalArgument(QStringLiteral("page"),
                                 QStringLiteral("The page to open: overview, clipboard, files, notifications, "
                                                "messages, or commands. With --snapshot: the screens to render."),
                                 QStringLiteral("[page]"));
    parser.process(app);
    const QString page = parser.positionalArguments().value(0);

    SingleInstance instance;
    if (instance.forward(page))
        return 0;
    if (!instance.listen())
        qWarning("flux-gui: cannot listen on %s", qPrintable(SingleInstance::socketPath()));

    QQmlApplicationEngine engine;
    FluxBackend *backend = expose(engine);
    engine.rootContext()->setContextProperty(QStringLiteral("fluxInitialPage"), page);
    engine.rootContext()->setContextProperty(QStringLiteral("fluxStartHidden"), parser.isSet(QStringLiteral("hidden")));
    // An update replaces flux-gui and restarts fluxd. The window then
    // offers a restart into the new version.
    auto *self = new SelfWatch(&engine);
    engine.rootContext()->setContextProperty(QStringLiteral("fluxSelf"), self);
    QObject::connect(self, &SelfWatch::aboutToRestart, &instance, &SingleInstance::close);
    QObject::connect(backend, &FluxBackend::connectedChanged, self, &SelfWatch::check);
    engine.load(QUrl(QStringLiteral("qrc:/flux/app/Main.qml")));
    if (engine.rootObjects().isEmpty())
        return 1;
    auto *window = qobject_cast<QQuickWindow *>(engine.rootObjects().constFirst());
    if (!window)
        return 1;

    // A second flux-gui opens the window again. When fluxd is down, the
    // backend then tries to connect at once.
    QObject::connect(&instance, &SingleInstance::activate, window, [window, backend](const QString &page, const QString &token) {
        backend->retryNow();
        if (!page.isEmpty())
            QMetaObject::invokeMethod(window, "showPage", Q_ARG(QVariant, page));
        raise(window, token);
    });
    addTray(app, window, backend);
    if (const QString grab = qEnvironmentVariable("FLUX_GUI_GRAB"); !grab.isEmpty())
        grabAfterState(window, backend, grab);
    return app.exec();
}
