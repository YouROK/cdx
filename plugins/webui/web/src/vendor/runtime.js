import * as React from "react";
import * as ReactDOM from "react-dom";
import * as ReactDOMClient from "react-dom/client";

const defaultRuntime = Object.assign({}, React, ReactDOM, ReactDOMClient, {
  React,
  ReactDOM,
  ReactDOMClient
});

export default defaultRuntime;
export { React, ReactDOM, ReactDOMClient };

export {
  Activity,
  Children,
  Component,
  Fragment,
  Profiler,
  PureComponent,
  StrictMode,
  Suspense,
  ViewTransition,
  act,
  addTransitionType,
  cache,
  cacheSignal,
  captureOwnerStack,
  cloneElement,
  createContext,
  createElement,
  createRef,
  forwardRef,
  isValidElement,
  lazy,
  memo,
  startTransition,
  unstable_useCacheRefresh,
  use,
  useActionState,
  useCallback,
  useContext,
  useDebugValue,
  useDeferredValue,
  useEffect,
  useEffectEvent,
  useId,
  useImperativeHandle,
  useInsertionEffect,
  useLayoutEffect,
  useMemo,
  useOptimistic,
  useReducer,
  useRef,
  useState,
  useSyncExternalStore,
  useTransition,
  version
} from "react";

export { jsx, jsxs } from "react/jsx-runtime";
export { jsxDEV } from "react/jsx-dev-runtime";

export {
  createPortal,
  flushSync,
  preconnect,
  prefetchDNS,
  preinit,
  preinitModule,
  preload,
  preloadModule,
  requestFormReset,
  unstable_batchedUpdates,
  useFormState,
  useFormStatus
} from "react-dom";

export { createRoot, hydrateRoot } from "react-dom/client";
