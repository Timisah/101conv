export type SetupStatus = {
  completed: boolean;
  administratorCreated: boolean;
};

export type SetupReadiness = {
  storage: {
    name: string;
    path: string;
    writable: boolean;
    issue?: string;
  }[];
  storageReady: boolean;
  installedEngines: number;
  installedProfiles: number;
  retention: string;
  workerWorkspaceLimitBytes: number;
};
