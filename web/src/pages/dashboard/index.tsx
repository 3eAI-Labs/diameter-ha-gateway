import React from "react";
import { Card, CardContent, Typography, Grid } from "@mui/material";

export const Dashboard: React.FC = () => {
  return (
    <Grid container spacing={2}>
      <Grid item xs={12} md={4}>
        <Card>
          <CardContent>
            <Typography color="textSecondary" gutterBottom>
              Active Peers
            </Typography>
            <Typography variant="h5" component="h2">
              3
            </Typography>
          </CardContent>
        </Card>
      </Grid>
      <Grid item xs={12} md={4}>
        <Card>
          <CardContent>
            <Typography color="textSecondary" gutterBottom>
              Total Requests (TPS)
            </Typography>
            <Typography variant="h5" component="h2">
              1,250
            </Typography>
          </CardContent>
        </Card>
      </Grid>
    </Grid>
  );
};
