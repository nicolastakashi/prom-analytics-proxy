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

const GetSerieLabels = async (name: string): Promise<string[]> => {
    try {
        const response = await axios.get(`${api}/serieLabels/${name}`);
        return response.data.filter((label: string) => label !== '__name__');
    } catch (error) {
        throw error;
    }
}

export default {
    Queries,
    QueryShortcuts,
    GetSeriesMetadata,
    GetSerieLabels,
};