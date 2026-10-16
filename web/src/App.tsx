import React from "react";
import { Refine } from "@refinedev/core";
import { ThemedLayoutV2, ErrorComponent } from "@refinedev/mui";
import { CssBaseline, GlobalStyles, ThemeProvider } from "@mui/material";
import routerBindings, { NavigateToResource, UnsavedChangesNotifier } from "@refinedev/react-router-v6";
import dataProvider from "@refinedev/simple-rest";
import { BrowserRouter, Route, Routes, Outlet } from "react-router-dom";
import { Dashboard } from "./pages/dashboard";
import { PeerList } from "./pages/peers";
import { theme } from "ui-kit";
import { Logo } from "ui-kit";

const App: React.FC = () => {
  return (
    <BrowserRouter>
      <ThemeProvider theme={theme}>
        <CssBaseline />
        <GlobalStyles styles={{ html: { WebkitFontSmoothing: "auto" } }} />
        <Refine
          dataProvider={dataProvider("/api/admin/v1")}
          routerProvider={routerBindings}
          resources={[
            {
              name: "dashboard",
              list: "/dashboard",
              meta: { label: "Dashboard" }
            },
            {
              name: "peers",
              list: "/peers",
              meta: { label: "Peers" }
            }
          ]}
          options={{
            syncWithLocation: true,
            warnWhenUnsavedChanges: true,
          }}
        >
          <Routes>
            <Route
              element={
                <ThemedLayoutV2
                  Title={({ collapsed }) => <Logo collapsed={collapsed} />}
                >
                  <Outlet />
                </ThemedLayoutV2>
              }
            >
              <Route index element={<NavigateToResource resource="dashboard" />} />
              <Route path="/dashboard" element={<Dashboard />} />
              <Route path="/peers" element={<PeerList />} />
              <Route path="*" element={<ErrorComponent />} />
            </Route>
          </Routes>
          <UnsavedChangesNotifier />
        </Refine>
      </ThemeProvider>
    </BrowserRouter>
  );
};

export default App;
