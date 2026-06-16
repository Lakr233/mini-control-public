import AppKit
import Darwin
import Logging
import SwiftUI

public enum SKL_CGSSpaceLevel: Int32 {
    case defaultLevel = 0
    case setupAssistant = 100
    case securityAgent = 200
    case screenLock = 300
    case notificationCenterAtScreenLock = 400
    case bootProgress = 500
    case voiceOver = 600
}

@MainActor
private final class SkyLightOperator {
    static let shared = SkyLightOperator()

    private typealias MainConnectionIDFunc = @convention(c) () -> Int32
    private typealias SpaceCreateFunc = @convention(c) (Int32, Int32, Int32) -> Int32
    private typealias SpaceSetAbsoluteLevelFunc = @convention(c) (Int32, Int32, Int32) -> Int32
    private typealias ShowSpacesFunc = @convention(c) (Int32, CFArray) -> Int32
    private typealias SpaceAddWindowsFunc = @convention(c) (Int32, Int32, CFArray, Int32) -> Int32

    private let connection: Int32
    private let space: Int32
    private let spaceAddWindowsAndRemoveFromSpaces: SpaceAddWindowsFunc

    private init() {
        let handle = dlopen(
            "/System/Library/PrivateFrameworks/SkyLight.framework/Versions/A/SkyLight", RTLD_NOW,
        )
        precondition(handle != nil, "failed to load SkyLight")

        let mainConnectionID = unsafeBitCast(
            dlsym(handle, "SLSMainConnectionID"), to: MainConnectionIDFunc.self,
        )
        let spaceCreate = unsafeBitCast(dlsym(handle, "SLSSpaceCreate"), to: SpaceCreateFunc.self)
        let spaceSetAbsoluteLevel = unsafeBitCast(
            dlsym(handle, "SLSSpaceSetAbsoluteLevel"), to: SpaceSetAbsoluteLevelFunc.self,
        )
        let showSpaces = unsafeBitCast(dlsym(handle, "SLSShowSpaces"), to: ShowSpacesFunc.self)
        spaceAddWindowsAndRemoveFromSpaces = unsafeBitCast(
            dlsym(handle, "SLSSpaceAddWindowsAndRemoveFromSpaces"), to: SpaceAddWindowsFunc.self,
        )

        connection = mainConnectionID()
        space = spaceCreate(connection, 1, 0)
        _ = spaceSetAbsoluteLevel(
            connection,
            space,
            SKL_CGSSpaceLevel.notificationCenterAtScreenLock.rawValue,
        )
        _ = showSpaces(connection, [space] as CFArray)
    }

    func delegateWindow(_ window: NSWindow) {
        _ = spaceAddWindowsAndRemoveFromSpaces(
            connection,
            space,
            [window.windowNumber] as CFArray,
            7,
        )
    }
}

private final class TopmostWindow: NSWindow {
    override init(
        contentRect: NSRect,
        styleMask: NSWindow.StyleMask,
        backing: NSWindow.BackingStoreType,
        defer flag: Bool,
    ) {
        super.init(
            contentRect: contentRect,
            styleMask: styleMask,
            backing: backing,
            defer: flag,
        )

        isOpaque = false
        alphaValue = 1
        titleVisibility = .hidden
        titlebarAppearsTransparent = true
        backgroundColor = .clear
        isMovable = false
        collectionBehavior = [
            .fullScreenAuxiliary,
            .stationary,
            .canJoinAllSpaces,
            .ignoresCycle,
        ]
        hasShadow = false
        canBecomeVisibleWithoutLogin = true
        ignoresMouseEvents = true
        level = .init(rawValue: .init(Int32.max - 2))
    }

    override var canBecomeKey: Bool {
        true
    }

    override var canBecomeMain: Bool {
        true
    }
}

private final class TopmostWindowController: NSWindowController {
    init(screen: NSScreen) {
        let window = TopmostWindow(
            contentRect: screen.frame,
            styleMask: [.borderless, .fullSizeContentView],
            backing: .buffered,
            defer: false,
        )
        super.init(window: window)
    }

    @available(*, unavailable)
    required init?(coder _: NSCoder) {
        fatalError()
    }
}

@MainActor
private final class OverlayViewModel: ObservableObject {
    @Published var hostname: String = ""
    @Published var version: String = ""
    @Published var updatedAt: Date = .init()
    @Published var vmInfos: [OverlaySnapshotVM] = []
    @Published var logLines: [String] = []
    @Published var connectionError: String?

    func apply(snapshot: OverlaySnapshot) {
        hostname = snapshot.hostname
        version = snapshot.version
        updatedAt = snapshot.updatedAt
        vmInfos = snapshot.vmInfos
        logLines = snapshot.logLines
        connectionError = nil
    }

    func showConnectionError(_ message: String) {
        connectionError = message
        logLines = [message]
        updatedAt = .init()
    }
}

private struct OverlayCardView: View {
    @ObservedObject var model: OverlayViewModel

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            header

            VStack(alignment: .leading, spacing: 8) {
                ForEach(displayLogLines.indices, id: \.self) { idx in
                    Text(displayLogLines[idx])
                        .font(.system(size: 11, weight: .regular, design: .monospaced))
                        .foregroundStyle(Color.white.opacity(0.9))
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .textSelection(.disabled)
                        .lineLimit(4)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .padding(14)
            .background(
                Color.black.opacity(0.18), in: RoundedRectangle(cornerRadius: 16, style: .continuous),
            )
        }
        .padding(18)
        .background {
            RoundedRectangle(cornerRadius: 28, style: .continuous)
                .fill(.ultraThinMaterial)
                .overlay(
                    RoundedRectangle(cornerRadius: 28, style: .continuous)
                        .strokeBorder(Color.white.opacity(0.14), lineWidth: 1),
                )
                .shadow(color: .black.opacity(0.22), radius: 24, x: 0, y: 18)
        }
        .frame(maxWidth: 760, alignment: .leading)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .center, spacing: 12) {
                Text(model.hostname.isEmpty ? "mini-control" : model.hostname)
                    .font(.system(size: 22, weight: .semibold, design: .rounded))
                    .foregroundStyle(.white)

                Circle()
                    .fill(
                        model.connectionError == nil ? Color.green.opacity(0.95) : Color.orange.opacity(0.95),
                    )
                    .frame(width: 8, height: 8)

                Text(statusText)
                    .font(.system(size: 11, weight: .medium, design: .monospaced))
                    .foregroundStyle(Color.white.opacity(0.72))

                Spacer()

                Text(timestampText)
                    .font(.system(size: 12, weight: .medium, design: .monospaced))
                    .foregroundStyle(Color.white.opacity(0.72))
            }

            Text(model.version.isEmpty ? "waiting for worker state" : summaryText)
                .font(.system(size: 11, weight: .medium, design: .monospaced))
                .foregroundStyle(Color.white.opacity(0.62))
        }
    }

    private var displayLogLines: [String] {
        let lines = model.logLines.suffix(8)
        return lines.isEmpty ? ["waiting for worker logs..."] : Array(lines)
    }

    private var statusText: String {
        model.connectionError == nil ? "online" : "error"
    }

    private var summaryText: String {
        guard model.connectionError == nil else {
            return model.connectionError ?? "overlay unavailable"
        }

        let total = model.vmInfos.count
        let ready = model.vmInfos.count(where: {
            ["ready", "running"].contains($0.state.lowercased())
        })
        let busy = model.vmInfos.count(where: {
            $0.state.caseInsensitiveCompare("busy") == .orderedSame
        })
        let starting = model.vmInfos.count(where: {
            ["starting", "bootstrapping", "destroying", "stopping", "deleting"].contains(
                $0.state.lowercased(),
            )
        })

        if total == 0 {
            return model.version.isEmpty ? "waiting for worker state" : "worker v\(model.version)"
        }

        return
            "worker v\(model.version)   total \(total)   ready \(ready)   busy \(busy)   changing \(starting)"
    }

    private var timestampText: String {
        Self.dateFormatter.string(from: model.updatedAt)
    }

    private static let dateFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateFormat = "HH:mm:ss"
        return formatter
    }()
}

private struct OverlayRootView: View {
    @ObservedObject var model: OverlayViewModel

    var body: some View {
        GeometryReader { geometry in
            ZStack(alignment: .bottomLeading) {
                Color.clear

                OverlayCardView(model: model)
                    .frame(maxWidth: min(geometry.size.width - 48, 760), alignment: .leading)
                    .padding(.leading, 28)
                    .padding(.bottom, 28)
                    .frame(
                        maxWidth: geometry.size.width, maxHeight: geometry.size.height,
                        alignment: .bottomLeading,
                    )
            }
        }
        .ignoresSafeArea()
        .allowsHitTesting(false)
    }
}

@MainActor
public final class LockScreenOverlay: Sendable {
    private let logger: Logger
    private let model = OverlayViewModel()
    private var windowControllers: [TopmostWindowController] = []
    private var pollTimer: Timer?
    private var waitingForScreens = false

    public init(logger: Logger) {
        self.logger = logger
    }

    public func start() {
        stop()
        waitingForScreens = false
        pollTimer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.refreshWindows()
            }
        }
        ensureWindows()
    }

    public func render(snapshot: OverlaySnapshot) {
        model.apply(snapshot: snapshot)
        ensureWindows()
        refreshWindows()
    }

    public func renderConnectionError(_ message: String) {
        model.showConnectionError(message)
        ensureWindows()
        refreshWindows()
    }

    public func stop() {
        pollTimer?.invalidate()
        pollTimer = nil
        waitingForScreens = false
        for controller in windowControllers {
            controller.close()
        }
        windowControllers.removeAll()
        logger.info("LockScreenOverlay: stopped")
    }

    private func ensureWindows() {
        if !windowControllers.isEmpty {
            let screens = NSScreen.screens
            if screens.count == windowControllers.count {
                return
            }

            for controller in windowControllers {
                controller.close()
            }
            windowControllers.removeAll()
        }

        let screens = NSScreen.screens
        guard !screens.isEmpty else {
            if !waitingForScreens {
                waitingForScreens = true
                logger.warning("LockScreenOverlay: no screens available")
            }
            return
        }
        waitingForScreens = false

        for screen in screens {
            let controller = TopmostWindowController(screen: screen)
            guard let window = controller.window else { continue }
            window.contentViewController = NSHostingController(rootView: OverlayRootView(model: model))
            window.setFrame(screen.frame, display: true)
            window.isReleasedWhenClosed = false
            SkyLightOperator.shared.delegateWindow(window)
            window.orderFrontRegardless()
            windowControllers.append(controller)
        }
        logger.info("LockScreenOverlay: started")
    }

    private func refreshWindows() {
        ensureWindows()
        for controller in windowControllers {
            controller.window?.orderFrontRegardless()
            controller.window?.displayIfNeeded()
        }
    }
}
