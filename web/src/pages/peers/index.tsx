import React from "react";
import { useList } from "@refinedev/core";
import { DataGrid, GridColDef } from "@mui/x-data-grid";
import { List } from "@refinedev/mui";

export const PeerList: React.FC = () => {
  const { data, isLoading } = useList({ resource: "peers" });

  const columns: GridColDef[] = [
    { field: "ip", headerName: "IP Address", flex: 1 },
    { field: "host", headerName: "Origin Host", flex: 1 },
    { field: "status", headerName: "Status", flex: 0.5 },
  ];

  // Mock data mapping if backend returns simple array
  const rows = data?.data?.map((item: any, index: number) => ({
    id: index,
    ...item
  })) || [];

  return (
    <List>
      <DataGrid 
        rows={rows} 
        columns={columns} 
        autoHeight 
        loading={isLoading}
      />
    </List>
  );
};
