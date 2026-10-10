// Clients for the hub's API, sending the web token with every call.
import { type Client, Code, ConnectError, createClient, type Interceptor } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { AgentService } from './gen/agora/v1/agents_pb';
import { BridgeService } from './gen/agora/v1/bridges_pb';
import { GovernanceService } from './gen/agora/v1/governance_pb';
import { ResourceService } from './gen/agora/v1/resources_pb';
import { RoomService } from './gen/agora/v1/rooms_pb';
import { WebService } from './gen/agora/v1/web_pb';

export type Api = {
  agents: Client<typeof AgentService>;
  rooms: Client<typeof RoomService>;
  resources: Client<typeof ResourceService>;
  governance: Client<typeof GovernanceService>;
  bridges: Client<typeof BridgeService>;
  web: Client<typeof WebService>;
};

export function makeApi(token: string, baseUrl = location.origin): Api {
  const auth: Interceptor = (next) => (req) => {
    req.header.set('Authorization', `Bearer ${token}`);
    return next(req);
  };
  const transport = createConnectTransport({ baseUrl, interceptors: [auth] });
  return {
    agents: createClient(AgentService, transport),
    rooms: createClient(RoomService, transport),
    resources: createClient(ResourceService, transport),
    governance: createClient(GovernanceService, transport),
    bridges: createClient(BridgeService, transport),
    web: createClient(WebService, transport),
  };
}

export function unauthenticated(err: unknown): boolean {
  return ConnectError.from(err).code === Code.Unauthenticated;
}
