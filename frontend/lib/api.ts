const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ? 
  `${process.env.NEXT_PUBLIC_API_URL}/api` : 
  'http://localhost:8080/api'

export interface TokenUsage {
  total_tokens: number
  input_tokens: number
  output_tokens: number
  usage_limit: number
  usage_rate: number
  window_start: string
  window_end: string
  active_sessions: number
  total_cost: number
  total_messages: number
}

export interface Session {
  id: string
  project_name: string
  project_path: string
  start_time: string
  end_time: string | null
  total_input_tokens: number
  total_output_tokens: number
  total_tokens: number
  message_count: number
  status: string
  created_at: string
  duration?: number
  is_active: boolean
  last_activity: string
  generated_code: string[]
}

export interface Message {
  id: string
  session_id: string
  parent_uuid: string | null
  is_sidechain: boolean
  user_type: string | null
  message_type: string | null
  message_role: string | null
  model: string | null
  content: string | null
  input_tokens: number
  cache_creation_input_tokens: number
  cache_read_input_tokens: number
  output_tokens: number
  service_tier: string | null
  request_id: string | null
  timestamp: string
  created_at: string
}

export interface PaginatedMessages {
  messages: Message[]
  total: number
  page: number
  page_size: number
  total_pages: number
  has_next: boolean
  has_previous: boolean
}

export interface SessionDetail {
  session: Session
  messages: Message[] | PaginatedMessages
  token_usage: TokenUsage
}

export interface ApiResponse<T> {
  data?: T
  error?: string
  message?: string
}

class ApiClient {
  private baseURL: string

  constructor(baseURL: string) {
    this.baseURL = baseURL
  }

  private async request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
    const url = `${this.baseURL}${endpoint}`
    
    const response = await fetch(url, {
      headers: {
        'Content-Type': 'application/json',
        ...options.headers,
      },
      ...options,
    })

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }

    return response.json()
  }

  async getTokenUsage(): Promise<TokenUsage> {
    return this.request<TokenUsage>('/token-usage')
  }

  async getSessions(): Promise<{ sessions: Session[], count: number }> {
    return this.request<{ sessions: Session[], count: number }>('/claude/sessions/recent')
  }

  async getSessionDetail(sessionId: string, page?: number, pageSize?: number): Promise<SessionDetail> {
    let url = `/sessions/${sessionId}`
    if (page !== undefined || pageSize !== undefined) {
      const params = new URLSearchParams()
      if (page !== undefined) params.append('page', page.toString())
      if (pageSize !== undefined) params.append('page_size', pageSize.toString())
      url += `?${params.toString()}`
    }
    return this.request<SessionDetail>(url)
  }

  async getAvailableTokens(plan: string = 'pro'): Promise<{
    available_tokens: number
    plan: string
    usage_limit: number
    used_tokens: number
  }> {
    return this.request(`/claude/available-tokens?plan=${plan}`)
  }

  async getCurrentMonthCosts(): Promise<{
    current_month_cost: number
    currency: string
    note: string
  }> {
    return this.request('/costs/current-month')
  }

  async getTasks(): Promise<{
    tasks: unknown[]
    count: number
    note: string
  }> {
    return this.request('/tasks')
  }

  async syncLogs(): Promise<{ message: string }> {
    return this.request('/sync-logs', { method: 'POST' })
  }

}

export const apiClient = new ApiClient(API_BASE_URL)

export const api = {
  tokenUsage: {
    getCurrent: () => apiClient.getTokenUsage(),
    getAvailable: (plan?: string) => apiClient.getAvailableTokens(plan),
  },
  sessions: {
    getAll: () => apiClient.getSessions(),
    getById: (id: string, page?: number, pageSize?: number) => apiClient.getSessionDetail(id, page, pageSize),
  },
  costs: {
    getCurrentMonth: () => apiClient.getCurrentMonthCosts(),
  },
  tasks: {
    getAll: () => apiClient.getTasks(),
  },
  sync: {
    logs: () => apiClient.syncLogs(),
  },
}