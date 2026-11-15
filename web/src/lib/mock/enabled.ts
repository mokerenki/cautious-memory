// Mock toggle. Public variables injected at build time (only the NEXT_PUBLIC_ prefix is readable in the browser).
// Set NEXT_PUBLIC_MOCK=1 on Vercel to run the entire site with mock data, without a backend.
export const MOCK = process.env.NEXT_PUBLIC_MOCK === "1";
