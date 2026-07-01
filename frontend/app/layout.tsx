import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "ping",
  description: "Self-hostable cron-job + uptime monitor",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
