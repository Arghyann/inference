import type { Metadata } from 'next'
import { Geist, Geist_Mono } from 'next/font/google'
import { Analytics } from '@vercel/analytics/next'
import './globals.css'

const _geist = Geist({ subsets: ["latin"] });
const _geistMono = Geist_Mono({ subsets: ["latin"] });

export const metadata: Metadata = {
  title: 'Aryan AI | Private Inference Workspace',
  description: 'A private chat workspace powered by the Aryan AI inference backend.',
  generator: 'v0.app',
  icons: {
    icon: [
      {
        url: '/icons8-favicon-doodle-16.png',
        sizes: '16x16',
        type: 'image/png',
      },
      {
        url: '/icons8-favicon-doodle-96.png',
        sizes: '96x96',
        type: 'image/png',
      },
    ],
    apple: '/icons8-favicon-doodle-96.png',
  },
}

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode
}>) {
  return (
    <html lang="en">
      <body className="font-sans antialiased">
        {children}
        {process.env.NODE_ENV === 'production' && <Analytics />}
      </body>
    </html>
  )
}
