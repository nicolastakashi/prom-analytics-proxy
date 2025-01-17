import axios from 'axios';

const api = `http://localhost:9091/api/v1`;
export interface QueryResult {
    columns: string[];
    data: Array<any>;
}
const Queries = async (query: string) => {
    const params = new URLSearchParams();
    params.append('query', query);

    try {
        const response = await axios.get(`${api}/queries`, { params });
        return response.data;
    } catch (error) {
        throw error;
    }
};

export interface QueryShortcut {
    title: string;
    query: string;
}

export interface PagedResult<T> {
    data: T[];
    total: number;
    totalPages: number;
}

const QueryShortcuts = async () => {
    try {
        const response = await axios.get(`${api}/queryShortcuts`);
        return response.data;
    } catch (error) {
        throw error;
    }
}

export interface SeriesMetadata {
    name: string;
    type: string;
    help: string;
}

const GetSeriesMetadata = async (): Promise<SeriesMetadata[]> => {
    try {
        const response = await axios.get(`${api}/seriesMetadata`);
        const result: SeriesMetadata[] = [];
        for (const [name, metrics] of Object.entries<any>(response.data)) {
            if (metrics.length > 0) { // Check if the array has at least one item
                const metric = metrics[0]; // Take only the first item
                result.push({
                    name,
                    type: metric.type,
                    help: metric.help,
                });
            }
        }
        return result;
    } catch (error) {
        throw error;
    }
}

export interface SerieMetadata {
    labels: string[];
    seriesCount: number;
}

const GetSerieMetadata = async (name: string): Promise<SerieMetadata> => {
    try {
        const response = await axios.get(`${api}/serieMetadata/${name}`);
        return response.data;
    } catch (error) {
        throw error;
    }
}


export interface SerieExpression {
    queryParam: string;
    avgDuration: number;
    avgPeakySamples: number;
    maxPeakSamples: number;
    ts: string;
}

const GetSerieExpressions = async (name: string, page: number): Promise<PagedResult<SerieExpression>> => {
    try {
        const response = await axios.get(`${api}/serieExpressions/${name}?page=${page - 1}&pageSize=10`);
        return response.data;
    } catch (error) {
        throw error;
    }
}
export interface RuleUsage {
    serie: string;
    groupName: string;
    name: string;
    expression: string;
    kind: string;
    labels: string[];
}

export interface DashboardUsage {
    id: string;
    serie: string;
    title: string;
    url: string;
}

const GetSerieUsage = async <T>(name: string, page: number, kind: string): Promise<PagedResult<T>> => {
    try {
        const response = await axios.get(`${api}/serieUsage/${name}?page=${page - 1}&pageSize=10&kind=${kind}`);
        return response.data;
    } catch (error) {
        throw error;
    }
}

export default {
    Queries,
    QueryShortcuts,
    GetSeriesMetadata,
    GetSerieMetadata,
    GetSerieExpressions,
    GetSerieUsage
};