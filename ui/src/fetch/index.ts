import axios from 'axios';

const api = `http://localhost:9091/api/v1`;
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

const QueryShortcuts = async () => {
    try {
        const response = await axios.get(`${api}/queryShortcuts`);
        return response.data;
    } catch (error) {
        throw error;
    }
}

export default {
    Queries,
    QueryShortcuts
};