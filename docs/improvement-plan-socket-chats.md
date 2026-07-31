# Plan: Mejoras de Socket y Chats WebUI

## Oleada 1 — Socket resilience ✅
- [x] A: Reconnect strategy (jitter ±30%, infinite retries, maxDelay 30s, openQueue cap 50)
- [x] B: Connection resilience (visibilitychange listener, client-side ping/pong 15s/10s)
- [x] C: Backend constants extraction + buffer sizes (8KB read/write, SendChan 512)

## Oleada 2 — Chat UX ✅
- [x] D: Retry failed messages (failed flag, retry button, offline queue preserved)
- [x] E: Typing indicators end-to-end (backend broadcast, frontend throttle 2s, animated dots)
- [x] F: Message list virtualization (react-virtuoso, followOutput, startReached pagination)

## Oleada 3 — Refactors ✅
- [x] G: messageEventHandlers split into 7 domain modules (event-handlers/)
- [x] H: Group state con useReducer (useGroupState hook, skip re-render optimization)
- [x] I: useMessageIndex hook (O(1) lookup by ID, lastAssistant/lastTool indexes)

## Verification
- Go build: ✅ clean
- Go tests (pkg/channels): ✅ pass
- TypeScript: ✅ zero errors
- Frontend tests: 169 pass / 11 fail (pre-existing App/Routing failures)
- reconnect tests: 7/7 pass (updated for new defaults + jitter)

## Files Modified
- pkg/channels/websocket.go (constants, buffers, typing broadcast)
- web/src/services/ws/reconnect.ts (jitter, infinite retries, 30s cap)
- web/src/services/ws/reconnect.test.ts (updated for new behavior)
- web/src/services/ws/client.ts (ping/pong, visibility, queue cap, getters)
- web/src/services/ws/events.ts (typing command data)
- web/src/hooks/messageEventHandlers.ts (barrel re-export)
- web/src/hooks/event-handlers/ (7 new domain files)
- web/src/hooks/useMessages.ts (retryMessage, typing, useGroupState)
- web/src/hooks/useGroupState.ts (new)
- web/src/hooks/useMessageIndex.ts (new)
- web/src/hooks/useStreamQueues.ts (comment)
- web/src/hooks/useMessages.test.ts (mock update)
- web/src/hooks/useAppLogic.ts (threading)
- web/src/contexts/AppLogicContext.tsx (onRetry, typing)
- web/src/lib/types.ts (failed field)
- web/src/components/organisms/MessageList.tsx (Virtuoso, typing indicator, onRetry)
- web/src/components/organisms/MessageBubble.tsx (retry UI)
- web/src/components/molecules/ChatComposer.tsx (typing emission)
- web/package.json (react-virtuoso)

## Completed: 2026-07-31
