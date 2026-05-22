export type Plan = {
  id: string;
  name: string;
  activePasteLimit: number;
  activeStorageBytes: number;
  singleTextBytes: number;
  singleFileBytes: number;
  singlePasteBytes: number;
  attachmentsPerPasteLimit: number;
  maxRetentionSeconds: number;
  dailyUploadBytes: number;
  dailyShareDownloadBytes: number;
};

export type PlanCatalog = {
  plans: Plan[];
};

const fallbackCatalog: PlanCatalog = {
  plans: [
    {
      id: 'free',
      name: 'Free',
      activePasteLimit: 20,
      activeStorageBytes: 500 * 1024 * 1024,
      singleTextBytes: 256 * 1024,
      singleFileBytes: 25 * 1024 * 1024,
      singlePasteBytes: 50 * 1024 * 1024,
      attachmentsPerPasteLimit: 5,
      maxRetentionSeconds: 24 * 60 * 60,
      dailyUploadBytes: 1024 * 1024 * 1024,
      dailyShareDownloadBytes: 2 * 1024 * 1024 * 1024,
    },
    {
      id: 'plus',
      name: 'Plus',
      activePasteLimit: 500,
      activeStorageBytes: 50 * 1024 * 1024 * 1024,
      singleTextBytes: 2 * 1024 * 1024,
      singleFileBytes: 250 * 1024 * 1024,
      singlePasteBytes: 1024 * 1024 * 1024,
      attachmentsPerPasteLimit: 20,
      maxRetentionSeconds: 30 * 24 * 60 * 60,
      dailyUploadBytes: 20 * 1024 * 1024 * 1024,
      dailyShareDownloadBytes: 100 * 1024 * 1024 * 1024,
    },
    {
      id: 'pro',
      name: 'Pro',
      activePasteLimit: 5000,
      activeStorageBytes: 500 * 1024 * 1024 * 1024,
      singleTextBytes: 10 * 1024 * 1024,
      singleFileBytes: 2 * 1024 * 1024 * 1024,
      singlePasteBytes: 5 * 1024 * 1024 * 1024,
      attachmentsPerPasteLimit: 100,
      maxRetentionSeconds: 180 * 24 * 60 * 60,
      dailyUploadBytes: 200 * 1024 * 1024 * 1024,
      dailyShareDownloadBytes: 1024 * 1024 * 1024 * 1024,
    },
  ],
};

export async function fetchPlanCatalog(): Promise<PlanCatalog> {
  try {
    const response = await fetch('/api/v1/plans', {
      headers: {
        Accept: 'application/json',
      },
    });

    if (!response.ok) {
      return fallbackCatalog;
    }

    return (await response.json()) as PlanCatalog;
  } catch {
    return fallbackCatalog;
  }
}

