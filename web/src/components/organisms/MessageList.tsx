import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useAppLogicContext } from "../../contexts/AppLogicContext";
import { useAuthContext } from "../../contexts/AuthContext";
import { MessageBubble } from "../MessageBubble";

const SCROLL_THRESHOLD = 300;
const DEBOUNCE_MS = 150;

export function MessageList() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { apiUrl } = useAuthContext();
  const {
    messages,
    approvalRequest,
    approvalResult,
    onApprove,
    currentSessionKey,
    loadMore,
    hasMore,
    isLoadingMore,
  } = useAppLogicContext();
  const containerRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement | null>(null);
  const [showSentinel, setShowSentinel] = useState(false);
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const isLoadingMoreRef = useRef(false);
  const isScrollRestoringRef = useRef(false); // guard against scroll events during sentinel restoration
  const lastMessageCountRef = useRef(0);
  const forceScrollRef = useRef(false);

  const isNearBottom = useCallback(() => {
    const container = containerRef.current;
    if (!container) return true;
    return (
      container.scrollHeight - container.scrollTop - container.clientHeight <
      SCROLL_THRESHOLD
    );
  }, []);

  const scrollToBottomSmooth = useCallback(() => {
    const container = containerRef.current;
    if (!container) return;
    container.scrollTo({
      top: container.scrollHeight,
      behavior: "smooth",
    });
  }, []);

  const handleNavigateToSession = useCallback(
    (sessionKey: string) => {
      if (!currentSessionKey) return;
      navigate(
        `/chat/${encodeURIComponent(
          currentSessionKey
        )}/subagent/${encodeURIComponent(sessionKey)}`
      );
    },
    [currentSessionKey, navigate]
  );

  // Handle scroll to load more messages
  const handleScroll = useCallback(() => {
    const container = containerRef.current;
    if (
      !container ||
      !hasMore ||
      isLoadingMoreRef.current ||
      isScrollRestoringRef.current
    )
      return;

    // Check if user scrolled near the top
    if (container.scrollTop < SCROLL_THRESHOLD) {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current);
      }

      debounceTimerRef.current = setTimeout(() => {
        // Insert sentinel at current scroll position before loading
        setShowSentinel(true);
        isLoadingMoreRef.current = true;
        loadMore();
      }, DEBOUNCE_MS);
    }
  }, [hasMore, loadMore]);

  // Restore scroll position after loading older messages using sentinel
  // biome-ignore lint/correctness/useExhaustiveDependencies: messages.length change triggers scroll restore
  useEffect(() => {
    if (!isLoadingMore && isLoadingMoreRef.current) {
      // Set scroll-restoring guard BEFORE scrollIntoView to prevent
      // the resulting scroll event from triggering another loadMore.
      isScrollRestoringRef.current = true;
      // Restore scroll using sentinel
      if (sentinelRef.current) {
        sentinelRef.current.scrollIntoView();
        setShowSentinel(false);
      }
      // Clear guards after scroll restoration is complete.
      // Use rAF to ensure the scroll event has been processed.
      window.requestAnimationFrame(() => {
        isScrollRestoringRef.current = false;
        isLoadingMoreRef.current = false;
      });
    }
  }, [isLoadingMore, messages.length]);

  // Track new messages and decide if we should scroll to bottom
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    // Check if new messages were added (not from loading more)
    if (
      messages.length > lastMessageCountRef.current &&
      !isLoadingMoreRef.current
    ) {
      const lastMessage = messages[messages.length - 1];
      // Only auto-scroll if it's a new user message, streaming message,
      // or a complete assistant response
      if (lastMessage) {
        const shouldScroll =
          lastMessage.role === "user" ||
          lastMessage.streaming ||
          (lastMessage.role === "assistant" && !lastMessage.streaming);
        if (shouldScroll && isNearBottom()) {
          scrollToBottomSmooth();
        }
      }
    }
    lastMessageCountRef.current = messages.length;
  }, [messages, isNearBottom, scrollToBottomSmooth]);

  // Cleanup debounce timer
  useEffect(() => {
    return () => {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current);
      }
    };
  }, []);

  // Reset refs and mark for forced scroll when session changes
  // biome-ignore lint/correctness/useExhaustiveDependencies: refs are intentionally used for mutation
  useEffect(() => {
    isLoadingMoreRef.current = false;
    lastMessageCountRef.current = 0;
    setShowSentinel(false);
    forceScrollRef.current = true;
  }, [currentSessionKey]);

  // Force scroll to bottom when messages load after switching sessions
  useEffect(() => {
    if (forceScrollRef.current && messages.length > 0) {
      // Use requestAnimationFrame to ensure DOM has rendered the new session's messages
      window.requestAnimationFrame(() => {
        scrollToBottomSmooth();
        forceScrollRef.current = false;
      });
    }
  }, [messages.length, scrollToBottomSmooth]);

  if (messages.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-sm text-text-tertiary">{t("chat.emptyState")}</p>
      </div>
    );
  }

  const visibleMessages = messages.filter(
    (m) =>
      !m.content.startsWith("⚠️ GUIDANCE:") &&
      !m.content.startsWith("GUIDANCE:")
  );

  return (
    <div
      ref={containerRef}
      onScroll={handleScroll}
      className="mx-auto max-w-3xl space-y-1 overflow-y-auto h-full"
    >
      {showSentinel && <div ref={sentinelRef} />}
      {isLoadingMore && (
        <div className="flex justify-center py-2">
          <div className="h-5 w-5 animate-spin rounded-full border-2 border-primary border-t-transparent" />
        </div>
      )}
      {!isLoadingMore && hasMore && (
        <p className="text-center py-2 text-xs text-text-tertiary">
          {t("chat.scrollUpForMore")}
        </p>
      )}
      {visibleMessages.map((message, index) => (
        <MessageBubble
          key={message.id}
          message={message}
          isLast={index === visibleMessages.length - 1}
          onNavigateToSession={handleNavigateToSession}
          apiUrl={apiUrl}
        />
      ))}
      {approvalRequest && !approvalResult && (
        <div className="py-2">
          <div className="rounded-lg border border-border bg-background-primary p-4">
            <p className="text-sm font-medium text-text-secondary mb-2">
              {approvalRequest.command}
            </p>
            <p className="text-xs text-text-secondary mb-4">
              {approvalRequest.reason}
            </p>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => onApprove(true)}
                className="rounded-md bg-state-success-light px-3 py-1.5 text-xs text-state-success hover:bg-state-success-light/80"
              >
                {t("approval.approve")}
              </button>
              <button
                type="button"
                onClick={() => onApprove(false)}
                className="rounded-md bg-state-error-light px-3 py-1.5 text-xs text-state-error hover:bg-state-error-light/80"
              >
                {t("approval.reject")}
              </button>
            </div>
          </div>
        </div>
      )}
      {approvalResult && (
        <div className="py-2">
          <div
            className={`rounded-lg border p-4 ${
              approvalResult.approved
                ? "border-state-success bg-state-success-light/10"
                : "border-state-error bg-state-error-light/10"
            }`}
          >
            <div className="flex items-center gap-2">
              <span className="text-lg">
                {approvalResult.approved ? "✅" : "❌"}
              </span>
              <span
                className={`text-sm font-medium ${
                  approvalResult.approved
                    ? "text-state-success"
                    : "text-state-error"
                }`}
              >
                {approvalResult.approved
                  ? t("approval.commandApproved")
                  : t("approval.commandRejected")}
              </span>
            </div>
            {approvalResult.command && (
              <pre className="mt-2 text-xs text-text-secondary whitespace-pre-wrap break-all">
                {approvalResult.command}
              </pre>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
