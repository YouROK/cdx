import { createRoot } from "react-dom/client";
import { App } from "./App.jsx";
import { UIProvider } from "./ui.jsx";
import "../style.css";

createRoot(document.getElementById("root")).render(
  <UIProvider>
    <App />
  </UIProvider>
);
