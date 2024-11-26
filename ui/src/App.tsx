import { Button } from "./components/shadcn/button"
import { Moon, Sun } from "lucide-react"
import { useTheme } from './theme-provider'
import { Routes, Route, Link } from 'react-router-dom'
import Explore from "./pages/explore"
import Metrics from "./pages/metrics"
import MetricDetails from "./pages/metrics/detail"

export default function App() {
  const { setTheme, theme } = useTheme()

  const toggleDarkMode = () => {
    setTheme(theme === 'dark' ? 'light' : 'dark')
  }

  return (
    <div className={`min-h-screen bg-gray-200 dark:bg-gray-900`}>
      <div className="bg-background text-foreground">
        <header className="border-b border-border">
          <div className="flex items-center justify-between px-4 h-14">
            <div className="flex items-center space-x-3">
              <Link to="/" className="text-lg font-semibold">
                Prom Analytics Proxy
              </Link>
              <div className="flex items-center">
                <Button variant="ghost" asChild className="text-muted-foreground hover:text-foreground">
                  <Link to="/">Explore</Link>
                </Button>
                <Button variant="ghost" asChild className="text-muted-foreground hover:text-foreground">
                  <Link to="/series">Metrics</Link>
                </Button>
              </div>
            </div>
            <div className="flex items-center">
              <Button variant="ghost" size="icon" onClick={toggleDarkMode}>
                {theme == 'dark' ? <Sun className="h-5 w-5" /> : <Moon className="h-5 w-5" />}
              </Button>
            </div>
          </div>
        </header>

        <div className="flex flex-col min-h-[calc(100vh-3.5rem)]">
          <Routes>
            <Route path="/" element={<Explore />} />
            <Route path="/series" element={<Metrics />} />
            <Route path="/series/:id" element={<MetricDetails />} />
          </Routes>
        </div>
      </div>
    </div>
  )
}


